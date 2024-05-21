package strategies

import (
	"context"
	"fmt"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/hash"
	"clerk/pkg/rand"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type Preparer interface {
	Identification() *model.Identification
	Prepare(context.Context, database.Tx) (*model.Verification, error)
}

type Attemptor interface {
	Attempt(context.Context, database.Tx) (*model.Verification, error)
	ToAPIError(error) apierror.Error
}

func AttemptVerification(ctx context.Context, tx database.Tx, attemptor Attemptor, verificationRepo *repository.Verification, clientID string) (*model.Verification, error) {
	verification, err := attemptor.Attempt(ctx, tx)
	if err != nil {
		return verification, err
	}

	// verification was successful and there is a client id
	if !verification.VerifiedAtClientID.Valid && clientID != "" {
		verification.VerifiedAtClientID = null.StringFrom(clientID)
		if err := verificationRepo.UpdateVerifiedAtClientID(ctx, tx, verification); err != nil {
			return verification, fmt.Errorf("attemptVerification: updating verified at client %s for verification %s: %w",
				clientID, verification.ID, err)
		}
	}

	return verification, nil
}

type createVerificationParams struct {
	instanceID               string
	strategy                 string
	nonce                    *string
	token                    *string
	identificationID         *string
	externalAuthorizationURL *string
}

func createVerification(
	ctx context.Context,
	tx database.Tx,
	clock clockwork.Clock,
	params *createVerificationParams,
) (*model.Verification, error) {
	verification := &model.Verification{Verification: &sqbmodel.Verification{
		InstanceID:               params.instanceID,
		Strategy:                 params.strategy,
		Attempts:                 0,
		ExpireAt:                 clock.Now().UTC().Add(time.Second * time.Duration(constants.ExpiryTimeTransactional)),
		Nonce:                    null.StringFromPtr(params.nonce),
		Token:                    null.StringFromPtr(params.token),
		IdentificationID:         null.StringFromPtr(params.identificationID),
		ExternalAuthorizationURL: null.StringFromPtr(params.externalAuthorizationURL),
	}}

	if err := repository.NewVerification().Insert(ctx, tx, verification); err != nil {
		return nil, fmt.Errorf("createVerification: inserting new verification %+v: %w",
			verification, err)
	}
	return verification, nil
}

// checkVerificationStatus returns an error if the status is
// not "unverified".
func checkVerificationStatus(ctx context.Context, tx database.Tx, verificationService *verifications.Service, ver *model.Verification) error {
	status, err := verificationService.Status(ctx, tx, ver)
	if err != nil {
		return err
	}
	if status == constants.VERUnverified {
		return nil
	}
	switch status {
	case constants.VERFailed:
		return ErrFailed
	case constants.VERExpired:
		return ErrExpired
	case constants.VERVerified:
		return ErrAlreadyVerified
	default:
		return NewUnknownStatusError(status)
	}
}

func attemptOTPCode(
	ctx context.Context,
	tx database.Tx,
	verificationService *verifications.Service,
	verification *model.Verification,
	code string,
	verificationRepo *repository.Verification) error {
	if err := checkVerificationStatus(ctx, tx, verificationService, verification); err != nil {
		return err
	}

	isCodeValid := isOtpCodeValid(verification.Token, code)
	if err := logVerificationAttempt(ctx, tx, verificationRepo, verification, isCodeValid); err != nil {
		return err
	}

	if !isCodeValid {
		return ErrInvalidCode
	}

	return nil
}

func isOtpCodeValid(token null.String, code string) bool {
	if !token.Valid {
		return false
	}
	isValid, err := hash.Compare(hash.Bcrypt, code, token.String)
	return isValid && err == nil
}

// generateOtpCodeWithHash generates a new OTP key & returns its value
// (for use in emails & SMS) and the hash (for storage)
// take a bool to use a static testCode
func generateOtpCodeWithHash(inTest bool) (string, string, error) {
	var otpCode string
	if inTest {
		otpCode = "424242"
	} else {
		randOtpCode, err := rand.OTPCode()
		if err != nil {
			return "", "", err
		}

		otpCode = randOtpCode
	}

	otpCodeDigest, err := hash.GenerateBcryptHash(otpCode)
	if err != nil {
		return "", "", err
	}

	return otpCode, otpCodeDigest, nil
}

// logVerificationAttempt updates the provided model.Verification to reflect
// that an attempt to verify it has occurred.
func logVerificationAttempt(ctx context.Context, tx database.Tx, repo *repository.Verification, ver *model.Verification, shouldResetToken bool) error {
	ver.Attempts = ver.Attempts + 1
	toUpdate := []string{sqbmodel.VerificationColumns.Attempts}
	if shouldResetToken {
		ver.Token = null.StringFromPtr(nil)
		toUpdate = append(toUpdate, sqbmodel.VerificationColumns.Token)
	}
	return repo.Update(ctx, tx, ver, toUpdate...)
}
