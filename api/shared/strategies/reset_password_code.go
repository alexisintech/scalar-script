package strategies

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/shared/comms"
	"clerk/api/shared/user_profile"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/activity"
	"clerk/pkg/hash"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type ResetPasswordCodePreparer struct {
	clock          clockwork.Clock
	env            *model.Env
	identification *model.Identification

	strategy   string
	sourceType string
	sourceID   string

	commsService       *comms.Service
	userProfileService *user_profile.Service
	verificationRepo   *repository.Verification
}

func NewResetPasswordCodePreparer(deps clerk.Deps, env *model.Env, identification *model.Identification, strategy, sourceType, sourceID string) ResetPasswordCodePreparer {
	return ResetPasswordCodePreparer{
		clock:              deps.Clock(),
		env:                env,
		identification:     identification,
		strategy:           strategy,
		sourceType:         sourceType,
		sourceID:           sourceID,
		commsService:       comms.NewService(deps),
		userProfileService: user_profile.NewService(deps.Clock()),
		verificationRepo:   repository.NewVerification(),
	}
}

func (p ResetPasswordCodePreparer) Identification() *model.Identification {
	return p.identification
}

func (p ResetPasswordCodePreparer) Prepare(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	usingTestIdentification := p.env.AuthConfig.TestMode && p.identification.IsTestIdentification()

	otpCode, otpCodeDigest, err := generateOtpCodeWithHash(usingTestIdentification)
	if err != nil {
		return nil, fmt.Errorf("prepare: creating OTP for reset password code: %w", err)
	}

	verification, err := createVerification(ctx, tx, p.clock, &createVerificationParams{
		instanceID:       p.env.Instance.ID,
		strategy:         p.strategy,
		identificationID: &p.identification.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare: creating verification for reset password code: %w", err)
	}

	verification.Token = null.StringFrom(otpCodeDigest)
	if err := p.verificationRepo.UpdateToken(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("prepare: updating OTP for reset password code verification %+v: %w",
			verification, err)
	}

	// If using test identifications, don't send any OTP code notifications
	if usingTestIdentification {
		return verification, nil
	}

	switch p.strategy {
	case constants.VSResetPasswordEmailCode:
		deviceActivity := activity.FromContext(ctx)
		err := p.commsService.SendResetPasswordCodeEmail(ctx, tx, p.identification, otpCode, p.sourceType, p.sourceID, p.env, deviceActivity)
		if err != nil {
			return nil, fmt.Errorf("prepare: sending reset password code via email for %s: %w",
				p.identification.ID, err)
		}
	case constants.VSResetPasswordPhoneCode:
		err := p.commsService.SendResetPasswordCodeSMS(ctx, tx, p.identification, otpCode, p.sourceType, p.sourceID, p.env, verification.ID)
		if err != nil {
			return nil, fmt.Errorf("prepare: sending reset password code via sms for %s: %w",
				p.identification.ID, err)
		}
	default:
		return nil, fmt.Errorf("prepare: unexpected strategy type %s for reset password code",
			p.strategy)
	}

	return verification, nil
}

type ResetPasswordCodeAttemptor struct {
	code             string
	newPassword      *string
	passwordSettings usersettingsmodel.PasswordSettings
	signIn           *model.SignIn
	verification     *model.Verification

	verificationService *verifications.Service

	signInRepo       *repository.SignIn
	verificationRepo *repository.Verification
}

func NewResetPasswordCodeAttemptor(
	clock clockwork.Clock,
	signIn *model.SignIn,
	verification *model.Verification,
	code string,
	newPassword *string,
	passwordSettings usersettingsmodel.PasswordSettings) ResetPasswordCodeAttemptor {
	return ResetPasswordCodeAttemptor{
		code:                code,
		newPassword:         newPassword,
		passwordSettings:    passwordSettings,
		signIn:              signIn,
		verification:        verification,
		verificationService: verifications.NewService(clock),
		signInRepo:          repository.NewSignIn(),
		verificationRepo:    repository.NewVerification(),
	}
}

func (r ResetPasswordCodeAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	err := attemptOTPCode(ctx, tx, r.verificationService, r.verification, r.code, r.verificationRepo)
	if err != nil {
		return r.verification, err
	}

	if r.newPassword == nil {
		// if new password is not given, mark sign in that it needs a new password
		r.signIn.RequiresNewPassword = true
		err = r.signInRepo.UpdateRequireNewPassword(ctx, tx, r.signIn)
		if err != nil {
			return r.verification, err
		}
	} else {
		apiErr := validate.Password(ctx, *r.newPassword, param.Password.Name, r.passwordSettings)
		if apiErr != nil {
			return r.verification, apiErr
		}

		passwordDigest, err := hash.GenerateBcryptHash(*r.newPassword)
		if err != nil {
			return r.verification, err
		}

		r.signIn.RequiresNewPassword = false
		r.signIn.NewPasswordDigest = null.StringFrom(passwordDigest)
		err = r.signInRepo.UpdateResetPasswordColumns(ctx, tx, r.signIn)
		if err != nil {
			return r.verification, err
		}
	}

	return r.verification, nil
}

func (ResetPasswordCodeAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrInvalidCode) {
		return apierror.FormIncorrectCode(param.Code.Name)
	}
	return toAPIErrors(err)
}
