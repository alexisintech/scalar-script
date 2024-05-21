package strategies

import (
	"errors"
	"fmt"

	"clerk/api/apierror"
)

var (
	ErrFailed                         = errors.New("verification: failed")
	ErrExpired                        = errors.New("verification: expired")
	ErrAlreadyVerified                = errors.New("verification: already verified")
	ErrInvalidCode                    = errors.New("verification: invalid code")
	ErrInvalidStrategyForVerification = errors.New("verification: invalid strategy for verification")
	ErrInvalidWeb3Signature           = errors.New("verification: invalid web3 signature")
	ErrInvalidRedirectURL             = errors.New("verification: invalid redirect url")
)

type UnknownStatusError struct {
	Status string
}

func (err UnknownStatusError) Error() string {
	return fmt.Sprintf("verification: unknown status %s", err.Status)
}

func NewUnknownStatusError(status string) UnknownStatusError {
	return UnknownStatusError{status}
}

// toAPIErrors provides a simple mapping between a
// verifications related error and our apierror.APIErrors
func toAPIErrors(err error) apierror.Error {
	var unknownStatusErr *UnknownStatusError
	if apiErr, isAPIErr := apierror.As(err); isAPIErr {
		return apiErr
	} else if errors.As(err, &unknownStatusErr) {
		return apierror.VerificationUnknownStatus(unknownStatusErr.Status)
	} else if errors.Is(err, ErrFailed) {
		return apierror.VerificationFailed()
	} else if errors.Is(err, ErrExpired) {
		return apierror.VerificationExpired()
	} else if errors.Is(err, ErrAlreadyVerified) {
		return apierror.VerificationAlreadyVerified()
	} else if errors.Is(err, ErrInvalidStrategyForVerification) {
		return apierror.VerificationInvalidStrategy()
	}
	return apierror.Unexpected(err)
}
