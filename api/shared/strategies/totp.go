package strategies

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/pkg/totp"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/pquerna/otp"

	"github.com/jonboulle/clockwork"
)

type TOTPAttemptor struct {
	totpSecret   string
	code         string
	instanceID   string
	verification *model.Verification

	clock               clockwork.Clock
	verificationService *verifications.Service
	verificationRepo    *repository.Verification
}

func NewTOTPAttemptor(clock clockwork.Clock, verification *model.Verification, totpSecret, code, instanceID string) TOTPAttemptor {
	return TOTPAttemptor{
		totpSecret:          totpSecret,
		code:                code,
		instanceID:          instanceID,
		verification:        verification,
		clock:               clock,
		verificationService: verifications.NewService(clock),
		verificationRepo:    repository.NewVerification(),
	}
}

func (v TOTPAttemptor) Attempt(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	verification := v.verification

	if err := checkVerificationStatus(ctx, tx, v.verificationService, v.verification); err != nil {
		return verification, err
	}

	matches, err := totp.Validate(v.clock, v.totpSecret, v.code)
	if err != nil {
		if errors.Is(err, otp.ErrValidateInputInvalidLength) {
			return verification, fmt.Errorf("totp/attempt: given code has invalid length, expected 6 digits: %w", ErrInvalidCode)
		}

		return nil, fmt.Errorf("totp/attempt: error while trying to validate code: %w", err)
	}

	if err = logVerificationAttempt(ctx, tx, v.verificationRepo, v.verification, false); err != nil {
		return nil, fmt.Errorf("totp/attempt: error while updading verification attempts: %w", err)
	}

	if !matches {
		return verification, fmt.Errorf("totp/attempt: given code does not match expected: %w", ErrInvalidCode)
	}

	return verification, nil
}

func (TOTPAttemptor) ToAPIError(err error) apierror.Error {
	if errors.Is(err, ErrInvalidCode) {
		return apierror.FormIncorrectCode(param.Code.Name)
	}
	return toAPIErrors(err)
}
