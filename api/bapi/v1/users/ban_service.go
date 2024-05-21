package users

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/database"
)

// Ban marks the given user as banned. This terminates their active sessions (marks them as revoked)
// and prevents them from signing in again.
func (s *Service) Ban(ctx context.Context, userID string) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	// Load all of the sessions & revoke them
	activeSessions, err := s.clientDataService.FindAllUserSessions(ctx, env.Instance.ID, user.ID, &client_data.SessionFilterParams{
		ActiveOnly: true,
	})
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	for _, session := range activeSessions {
		session.Status = constants.SESSRevoked
		err = s.clientDataService.UpdateSessionStatus(ctx, session)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	// Ban the user
	var userResponse *serialize.UserResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		user.Banned = true
		err := s.userRepo.UpdateBanned(ctx, tx, user)
		if err != nil {
			return true, err
		}

		userSerializable, err := s.serializableService.ConvertUser(ctx, tx, userSettings, user)
		if err != nil {
			return true, err
		}
		userResponse = serialize.UserToServerAPI(ctx, userSerializable)

		if err = s.eventService.UserUpdated(ctx, tx, env.Instance, userResponse); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}
	return userResponse, nil
}

// Unban removes the ban from the given user.
func (s *Service) Unban(ctx context.Context, userID string) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	var userResponse *serialize.UserResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		user.Banned = false
		err = s.userRepo.UpdateBanned(ctx, s.db, user)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		userSerializable, err := s.serializableService.ConvertUser(ctx, tx, userSettings, user)
		if err != nil {
			return true, err
		}
		userResponse = serialize.UserToServerAPI(ctx, userSerializable)

		if err = s.eventService.UserUpdated(ctx, tx, env.Instance, userResponse); err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return userResponse, nil
}
