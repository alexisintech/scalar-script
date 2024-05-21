package users

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/database"
)

// Lock marks the provided user as locked
func (s *Service) Lock(ctx context.Context, userID string) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.UserLockoutEnabled() {
		return nil, apierror.FeatureNotEnabled()
	}

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	var userResponse *serialize.UserResponse

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		userSerializable, err := s.userLockoutService.Lock(ctx, tx, env, user)
		if err != nil {
			return true, err
		}

		userResponse = serialize.UserToServerAPI(ctx, userSerializable)

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}

		return nil, apierror.Unexpected(txErr)
	}

	return userResponse, nil
}

// Unlock unlocks the given user
func (s *Service) Unlock(ctx context.Context, userID string) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.UserLockoutEnabled() {
		return nil, apierror.FeatureNotEnabled()
	}

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	var userResponse *serialize.UserResponse

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		userSerializable, err := s.userLockoutService.Unlock(ctx, tx, env, user)
		if err != nil {
			return true, err
		}

		userResponse = serialize.UserToServerAPI(ctx, userSerializable)

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}

		return nil, apierror.Unexpected(txErr)
	}

	return userResponse, nil
}
