package users

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/database"
)

// DisableMFA disables all user's MFA methods
func (s *Service) DisableMFA(ctx context.Context, userID string) apierror.Error {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, env.Instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if user == nil {
		return apierror.UserNotFound(userID)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err = s.identRepo.RestoreSecondFactorByUser(ctx, tx, userID); err != nil {
			return true, err
		}

		if err = s.totpRepo.DeleteByUser(ctx, tx, userID); err != nil {
			return true, err
		}

		if err = s.backupCodeRepo.DeleteByUser(ctx, tx, userID); err != nil {
			return true, err
		}

		if err = s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, user); err != nil {
			return true, fmt.Errorf("user/disableMFA: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err)
		}

		return false, nil
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	return nil
}
