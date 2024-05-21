package users

import (
	"context"
	"mime/multipart"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/users"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
)

// UpdateProfileImage updates the user's profile image
func (s *Service) UpdateProfileImage(ctx context.Context, userID string, filePart *multipart.Part) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	_, apiErr := s.shUsersService.UpdateProfileImage(
		ctx,
		users.UpdateProfileImageParams{
			Data:     filePart,
			Filename: filePart.FileName(),
			UserID:   userID,
		},
		env.Instance,
		userSettings,
	)
	if apiErr != nil {
		return nil, apiErr
	}

	// respond with updated user
	user, err := s.userRepo.FindByID(ctx, s.db, userID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userSerializable, err := s.serializableService.ConvertUser(ctx, s.db, userSettings, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.UserToServerAPI(ctx, userSerializable), nil
}

// DeleteProfileImage deletes the user's profile image
func (s *Service) DeleteProfileImage(ctx context.Context, userID string) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	_, apiErr := s.shUsersService.DeleteProfileImage(ctx, userID)
	if apiErr != nil {
		return nil, apiErr
	}

	// respond with updated user
	user, err := s.userRepo.FindByID(ctx, s.db, userID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userSerializable, err := s.serializableService.ConvertUser(ctx, s.db, userSettings, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.UserToServerAPI(ctx, userSerializable), nil
}
