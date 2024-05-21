package users

import (
	"context"
	"errors"

	"github.com/pquerna/otp"

	"clerk/pkg/backup_codes"
	"clerk/pkg/totp"

	"clerk/api/apierror"
)

type VerifyTOTPParams struct {
	Code string `json:"code" form:"code" validate:"required"`
}

// VerifyTOTP returns an error if code is not a valid TOTP or backup code for
// the user.
func (s *Service) VerifyTOTP(ctx context.Context, userID, code string) (string, apierror.Error) {
	user, err := s.userRepo.QueryByID(ctx, s.db, userID)
	if err != nil {
		return "", apierror.Unexpected(err)
	}
	if user == nil {
		return "", apierror.UserNotFound(userID)
	}

	// if the user has backup codes enabled, try those first
	currentBackupCodes, err := s.backupCodeRepo.QueryByUser(ctx, s.db, userID)
	if err != nil {
		return "", apierror.Unexpected(err)
	}
	if currentBackupCodes != nil {
		updatedCodes, err := backup_codes.Consume(currentBackupCodes.Codes, code)
		if err == nil {
			currentBackupCodes.Codes = updatedCodes
			err = s.backupCodeRepo.UpdateCodes(ctx, s.db, currentBackupCodes)
			if err != nil {
				return "", apierror.Unexpected(err)
			}
			return "backup_code", nil
		}
	}

	// if the input doesn't succeed as a backup code, assume it's a TOTP code
	totpConfig, err := s.totpRepo.QueryVerifiedByUser(ctx, s.db, user.ID)
	if err != nil {
		return "", apierror.Unexpected(err)
	}
	if totpConfig == nil {
		return "", apierror.TOTPDisabled()
	}

	matches, err := totp.Validate(s.clock, totpConfig.Secret, code)
	if errors.Is(err, otp.ErrValidateInputInvalidLength) {
		return "", apierror.InvalidLengthTOTP()
	} else if err != nil {
		return "", apierror.Unexpected(err)
	}

	if !matches {
		return "", apierror.IncorrectTOTP()
	}

	return "totp", nil
}
