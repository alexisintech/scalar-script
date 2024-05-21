package bff

import (
	"context"
	"time"

	"clerk/api/apierror"

	"clerk/api/dapi/serializable"
	serialize "clerk/api/dapi/serialize/bff"
	"clerk/api/shared/pagination"

	"clerk/api/shared/user_profile"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/clerk/clerk-sdk-go/v2/user"
	"github.com/jonboulle/clockwork"
)

type Service struct {
	db           database.Database
	newSDKConfig sdkutils.ConfigConstructor

	// services
	serializableService *serializable.Service
	userProfileService  *user_profile.Service

	// repositories
	instanceKeysRepo *repository.InstanceKeys
	authConfigRepo   *repository.AuthConfig
	userRepo         *repository.Users
}

func NewService(
	db database.Database,
	clock clockwork.Clock,
	newSDKConfig sdkutils.ConfigConstructor,
) *Service {
	return &Service{
		db:                  db,
		newSDKConfig:        newSDKConfig,
		instanceKeysRepo:    repository.NewInstanceKeys(),
		authConfigRepo:      repository.NewAuthConfig(),
		userRepo:            repository.NewUsers(),
		serializableService: serializable.NewService(clock),
		userProfileService:  user_profile.NewService(clock),
	}
}

func (s *Service) APIKeys(ctx context.Context) (*serialize.APIKeysResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	keys, err := s.instanceKeysRepo.FindAllByInstance(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.APIKeys(env.Instance, env.Domain, keys), nil
}

type ListParams struct {
	orderBy         string
	query           string
	organizationIDs []string
}

var ValidUsersOrderByFields = set.New(
	sqbmodel.UserColumns.CreatedAt,
	sqbmodel.UserColumns.UpdatedAt,
	sqbmodel.UserColumns.LastSignInAt,
	repository.OrderFieldEmailAddress,
	repository.OrderFieldFirstName,
	repository.OrderFieldLastName,
	repository.OrderFieldUsername,
	repository.OrderFieldPhoneNumber,
)

func (l *ListParams) ToSDKListUsersParams() user.ListParams {
	var params user.ListParams
	if l.query != "" {
		params.Query = &l.query
	}
	return params
}

func (l *ListParams) ToUserMods() (repository.UsersFindAllModifiers, apierror.Error) {
	var mods repository.UsersFindAllModifiers

	mods.OrganizationIDs = repository.NewParamsWithExclusion(l.organizationIDs...)
	mods.Query = l.query

	if l.orderBy != "" {
		orderByField, err := repository.ConvertToOrderByField(l.orderBy, ValidUsersOrderByFields)
		if err != nil {
			return mods, err
		}
		mods.OrderBy = &orderByField
	}

	return mods, nil
}

func (s *Service) ListUsersWithSettings(ctx context.Context, instanceID string, params ListParams, pagination pagination.Params) (*serialize.ListUsersResponse, apierror.Error) {
	authConfig, err := s.authConfigRepo.FindByInstanceActiveAuthConfigID(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	userSettings := usersettings.NewUserSettings(authConfig.UserSettings)

	mods, apierr := params.ToUserMods()
	if apierr != nil {
		return nil, apierr
	}

	users, err := s.userRepo.FindAllWithModifiers(ctx, s.db, instanceID, mods, pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializableUsers, err := s.serializableService.ConvertUsers(ctx, s.db, userSettings, users)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	sdkParams := params.ToSDKListUsersParams()
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	totalCountResponse, apierr := sdkutils.WithRetry(func() (*user.TotalCount, apierror.Error) {
		userCount, err := sdkClient.Count(ctx, &sdkParams)
		return userCount, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})

	if apierr != nil {
		return nil, apierr
	}

	return serialize.ListUsers(ctx, serializableUsers, totalCountResponse), nil
}

func (s *Service) newSDKClientForInstance(ctx context.Context, instanceID string) (*user.Client, apierror.Error) {
	config, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return user.NewClient(config), nil
}
