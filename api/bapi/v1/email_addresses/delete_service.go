package email_addresses

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/database"
)

// Delete - DELETE /v1/email_addresses/:id
func (s *Service) Delete(ctx context.Context, emailAddressID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	emailAddress, apiErr := s.getEmailAddressInInstance(ctx, env.Instance.ID, emailAddressID)
	if apiErr != nil {
		return nil, apiErr
	}

	user, err := s.userRepo.FindByID(ctx, s.db, emailAddress.UserID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var deletedObject *serialize.DeletedObjectResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		deletedObject, err = s.shIdentificationsService.Delete(ctx, tx, env.Instance, userSettings, user, emailAddress)
		if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return deletedObject, nil
}
