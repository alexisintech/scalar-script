package strategies

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/shared/comms"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/activity"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type EmailCodePreparer struct {
	clock          clockwork.Clock
	env            *model.Env
	identification *model.Identification

	sourceType string
	sourceID   string

	commsService     *comms.Service
	verificationRepo *repository.Verification
}

func NewEmailCodePreparer(deps clerk.Deps, env *model.Env, identification *model.Identification, sourceType, sourceID string) EmailCodePreparer {
	return EmailCodePreparer{
		clock:            deps.Clock(),
		env:              env,
		identification:   identification,
		sourceType:       sourceType,
		sourceID:         sourceID,
		commsService:     comms.NewService(deps),
		verificationRepo: repository.NewVerification(),
	}
}

func (p EmailCodePreparer) Identification() *model.Identification {
	return p.identification
}

func (p EmailCodePreparer) Prepare(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	useTestEmailCode := p.env.AuthConfig.TestMode && p.identification.IsTestIdentification()

	otpCode, otpCodeDigest, err := generateOtpCodeWithHash(useTestEmailCode)
	if err != nil {
		return nil, fmt.Errorf("prepare: creating OTP digest for email code: %w", err)
	}

	verification, err := createVerification(ctx, tx, p.clock, &createVerificationParams{
		instanceID:       p.env.Instance.ID,
		strategy:         constants.VSEmailCode,
		identificationID: &p.identification.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare: creating verification for email code: %w", err)
	}

	verification.Token = null.StringFrom(otpCodeDigest)
	if err := p.verificationRepo.UpdateToken(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("prepare: updating OTP digest for email code verification %+v: %w",
			verification, err)
	}

	// Don't send the email for test emails
	if !useTestEmailCode {
		deviceActivity := activity.FromContext(ctx)
		if err := p.commsService.SendVerificationCodeEmail(ctx, tx, p.identification, otpCode, p.sourceType, p.sourceID, p.env, deviceActivity); err != nil {
			return nil, fmt.Errorf("prepare: sending email code for %+v: %w",
				p.identification, err)
		}
	}

	return verification, nil
}

type EmailCodeAttemptor struct {
	code         string
	verification *model.Verification

	verificationService *verifications.Service
	verificationRepo    *repository.Verification
}

func NewEmailCodeAttemptor(clock clockwork.Clock, verification *model.Verification, code string) EmailCodeAttemptor {
	return EmailCodeAttemptor{
		code:                code,
		verification:        verification,
		verificationService: verifications.NewService(clock),
		verificationRepo:    repository.NewVerification(),
	}
}

func (v EmailCodeAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	err := attemptOTPCode(ctx, tx, v.verificationService, v.verification, v.code, v.verificationRepo)
	return v.verification, err
}

func (EmailCodeAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrInvalidCode) {
		return apierror.FormIncorrectCode(param.Code.Name)
	}
	return toAPIErrors(err)
}
