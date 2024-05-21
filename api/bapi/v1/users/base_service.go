package users

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/events"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/api/shared/serializable"
	userlockout "clerk/api/shared/user_lockout"
	"clerk/api/shared/users"
	"clerk/api/shared/validators"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

// Service contains the business logic of all operations specific to users in server API.
type Service struct {
	db    database.Database
	clock clockwork.Clock

	// services
	clientDataService   *client_data.Service
	eventService        *events.Service
	orgsService         *organizations.Service
	serializableService *serializable.Service
	shUsersService      *users.Service
	userCreateService   *users.CreateService
	userLockoutService  *userlockout.Service
	validatorService    *validators.Service

	// repositories
	externalAccountRepo *repository.ExternalAccount
	identRepo           *repository.Identification
	orgMembershipsRepo  *repository.OrganizationMembership
	totpRepo            *repository.TOTP
	userRepo            *repository.Users
	verRepo             *repository.Verification
	backupCodeRepo      *repository.BackupCode
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                  deps.DB(),
		clock:               deps.Clock(),
		clientDataService:   client_data.NewService(deps),
		eventService:        events.NewService(deps),
		orgsService:         organizations.NewService(deps),
		validatorService:    validators.NewService(),
		serializableService: serializable.NewService(deps.Clock()),
		shUsersService:      users.NewService(deps),
		userCreateService:   users.NewCreateService(deps.Clock()),
		userLockoutService:  userlockout.NewService(deps),
		externalAccountRepo: repository.NewExternalAccount(),
		identRepo:           repository.NewIdentification(),
		orgMembershipsRepo:  repository.NewOrganizationMembership(),
		totpRepo:            repository.NewTOTP(),
		userRepo:            repository.NewUsers(),
		verRepo:             repository.NewVerification(),
		backupCodeRepo:      repository.NewBackupCode(),
	}
}

// CheckUserInInstance checks whether the given user id belongs to the current instance and returns an error if it doesn't
func (s *Service) CheckUserInInstance(ctx context.Context, userID string) apierror.Error {
	env := environment.FromContext(ctx)

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, env.Instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	} else if user == nil {
		return apierror.UserNotFound(userID)
	}
	return nil
}

type ListOrganizationMembershipsParams struct {
	UserID string
	Limit  string
	Offset string
}

// ListOrganizationMemberships returns a paginated list of serialized
// organization memberships for a user.
func (s *Service) ListOrganizationMemberships(
	ctx context.Context,
	params ListOrganizationMembershipsParams,
	paginationParams pagination.Params,
) (*serialize.PaginatedResponse, apierror.Error) {
	memberships, apiErr := s.orgsService.ListMemberships(ctx, s.db, organizations.ListMembershipsParams{
		UserID: &params.UserID,
	}, paginationParams)
	if apiErr != nil {
		return nil, apiErr
	}

	totalCount, err := s.orgMembershipsRepo.CountByUser(ctx, s.db, params.UserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responseData := make([]interface{}, len(memberships))
	for i, membership := range memberships {
		responseData[i] = serialize.OrganizationMembershipBAPI(ctx, membership)
	}
	return serialize.Paginated(responseData, totalCount), nil
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
