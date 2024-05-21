package users

import (
	"context"

	"clerk/api/apierror"
	"clerk/pkg/hash"
)

type VerifyPasswordParams struct {
	Password string `json:"password" form:"password" validate:"required"`
}

// VerifyPassword returns an error if password is not the one that the user set.
func (s *Service) VerifyPassword(ctx context.Context, userID, password string) apierror.Error {
	user, err := s.userRepo.QueryByID(ctx, s.db, userID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if user == nil {
		return apierror.UserNotFound(userID)
	}

	if !user.PasswordDigest.Valid {
		return apierror.NoPasswordSet()
	}

	matches, err := hash.Compare(
		user.PasswordHasher.String, password, user.PasswordDigest.String)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if !matches {
		return apierror.IncorrectPassword()
	}

	return nil
}
