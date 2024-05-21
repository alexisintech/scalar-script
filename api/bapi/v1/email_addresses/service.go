package email_addresses

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/identifications"
	"clerk/api/shared/serializable"
	"clerk/api/shared/user_profile"
	"clerk/api/shared/validators"
	"clerk/model"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"

	"github.com/jonboulle/clockwork"
)

// Service contains the business logic of all operations specific to users in server API.
type Service struct {
	clock     clockwork.Clock
	db        database.Database
	validator *validator.Validate

	// services
	eventService             *events.Service
	validatorService         *validators.Service
	serializableService      *serializable.Service
	shIdentificationsService *identifications.Service
	userProfileService       *user_profile.Service

	// repositories
	userRepo           *repository.Users
	identificationRepo *repository.Identification
	verificationRepo   *repository.Verification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                    deps.Clock(),
		db:                       deps.DB(),
		validator:                validator.New(),
		eventService:             events.NewService(deps),
		validatorService:         validators.NewService(),
		serializableService:      serializable.NewService(deps.Clock()),
		shIdentificationsService: identifications.NewService(deps),
		userProfileService:       user_profile.NewService(deps.Clock()),
		userRepo:                 repository.NewUsers(),
		identificationRepo:       repository.NewIdentification(),
		verificationRepo:         repository.NewVerification(),
	}
}

// Helpers
func (s *Service) getEmailAddressInInstance(ctx context.Context, instanceID string, emailAddressID string) (*model.Identification, apierror.Error) {
	emailAddress, err := s.identificationRepo.QueryByIDAndInstance(ctx, s.db, emailAddressID, instanceID, repository.RequireUserQueryOption)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if emailAddress == nil {
		return nil, apierror.IdentificationNotFound(emailAddressID)
	}

	return emailAddress, nil
}

func (s *Service) sendUserUpdatedEvent(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User) error {
	userSerializable, err := s.serializableService.ConvertUser(ctx, exec, userSettings, user)
	if err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: serializing user %+v: %w", user, err)
	}

	if err = s.eventService.UserUpdated(ctx, exec, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: send user updated event for user %s: %w", user.ID, err)
	}
	return nil
}
