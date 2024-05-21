package users

import (
	"context"
	"fmt"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/repository"
	"clerk/utils/database"
)

type CreateService struct {
	clock clockwork.Clock

	userRepo *repository.Users
}

func NewCreateService(clock clockwork.Clock) *CreateService {
	return &CreateService{
		clock:    clock,
		userRepo: repository.NewUsers(),
	}
}

type CreateParams struct {
	AuthConfig   *model.AuthConfig
	Instance     *model.Instance
	Subscription *model.Subscription
	User         *model.User
}

// Create creates and stores a new user with the given params.
// This function assumes that the given params are already validated.
func (s *CreateService) Create(ctx context.Context, tx database.Tx, params CreateParams) error {
	err := s.checkIfUserCreationIsAllowed(ctx, tx, params)
	if err != nil {
		return err
	}

	if params.User.PasswordDigest.Valid {
		params.User.PasswordLastUpdatedAt = null.TimeFrom(s.clock.Now().UTC())
	}

	params.User.DeleteSelfEnabled = params.AuthConfig.UserSettings.Actions.DeleteSelf
	params.User.CreateOrganizationEnabled = params.AuthConfig.UserSettings.Actions.CreateOrganization

	err = s.userRepo.Insert(ctx, tx, params.User)
	if err != nil {
		return fmt.Errorf("users/createService: create user %+v: %w",
			params.User, err)
	}

	return nil
}

func (s *CreateService) checkIfUserCreationIsAllowed(ctx context.Context, tx database.Tx, params CreateParams) error {
	if !params.AuthConfig.MaxAllowedUsers.Valid {
		return nil
	}

	usersInInstance, err := s.userRepo.CountForInstance(ctx, tx, params.Instance.ID)
	if err != nil {
		return err
	}

	if usersInInstance >= int64(params.AuthConfig.MaxAllowedUsers.Int) {
		return apierror.UserQuotaExceeded(params.AuthConfig.MaxAllowedUsers.Int, params.Instance.EnvironmentType)
	}
	return nil
}
