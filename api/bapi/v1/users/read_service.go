package users

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
)

// Read returns the user that is already loaded in the context.
// Keep in mind that the SetUser must be called before using this.
func (s *Service) Read(ctx context.Context, userID string) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(userID)
	}

	userSerializable, err := s.serializableService.ConvertUser(ctx, s.db, userSettings, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.UserToServerAPI(ctx, userSerializable), nil
}
