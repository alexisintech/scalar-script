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
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type PhoneCodePreparer struct {
	clock          clockwork.Clock
	env            *model.Env
	identification *model.Identification

	sourceType string
	sourceID   string

	commsService     *comms.Service
	verificationRepo *repository.Verification
}

func NewPhoneCodePreparer(deps clerk.Deps, env *model.Env, identification *model.Identification, sourceType, sourceID string) PhoneCodePreparer {
	return PhoneCodePreparer{
		clock:            deps.Clock(),
		env:              env,
		identification:   identification,
		sourceType:       sourceType,
		sourceID:         sourceID,
		commsService:     comms.NewService(deps),
		verificationRepo: repository.NewVerification(),
	}
}

func (p PhoneCodePreparer) Identification() *model.Identification {
	return p.identification
}

func (p PhoneCodePreparer) Prepare(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	useTestPhoneCode := p.env.AuthConfig.TestMode && p.identification.IsTestIdentification()

	otpCode, otpCodeDigest, err := generateOtpCodeWithHash(useTestPhoneCode)
	if err != nil {
		return nil, fmt.Errorf("prepare: creating OTP digest for phone code: %w", err)
	}

	verification, err := createVerification(ctx, tx, p.clock, &createVerificationParams{
		instanceID:       p.env.Instance.ID,
		strategy:         constants.VSPhoneCode,
		identificationID: &p.identification.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("prepare: creating verification for phone code: %w", err)
	}

	verification.Token = null.StringFrom(otpCodeDigest)
	if err := p.verificationRepo.UpdateToken(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("prepare: updating OTP digest for phone code verification %+v: %w",
			verification, err)
	}

	// Don't send the sms for test numbers
	if !useTestPhoneCode {
		if err := p.commsService.SendVerificationCodeSMS(ctx, tx, p.identification, otpCode, p.sourceType, p.sourceID, p.env, verification.ID); err != nil {
			return nil, fmt.Errorf("prepare: sending phone code for %+v: %w",
				p.identification, err)
		}
	}

	return verification, nil
}

type PhoneCodeAttemptor struct {
	code         string
	verification *model.Verification

	verificationService *verifications.Service
	verificationRepo    *repository.Verification
}

func NewPhoneCodeAttemptor(clock clockwork.Clock, verification *model.Verification, code string) PhoneCodeAttemptor {
	return PhoneCodeAttemptor{
		code:                code,
		verification:        verification,
		verificationService: verifications.NewService(clock),
		verificationRepo:    repository.NewVerification(),
	}
}

func (v PhoneCodeAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	err := attemptOTPCode(ctx, tx, v.verificationService, v.verification, v.code, v.verificationRepo)
	return v.verification, err
}

func (PhoneCodeAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrInvalidCode) {
		return apierror.FormIncorrectCode(param.Code.Name)
	}
	return toAPIErrors(err)
}
