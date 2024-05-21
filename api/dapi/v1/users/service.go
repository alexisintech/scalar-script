package users

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"clerk/api/apierror"
	"clerk/api/dapi/v1/authorization"
	"clerk/api/serialize"
	"clerk/api/shared/pagination"
	"clerk/api/shared/serializable"
	sharedusers "clerk/api/shared/users"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	clerkjson "clerk/pkg/json"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/actortoken"
	"github.com/clerk/clerk-sdk-go/v2/user"
)

type Service struct {
	userClient   *user.Client
	db           database.Database
	newSDKConfig sdkutils.ConfigConstructor

	// services
	serializableService *serializable.Service
	sharedUserService   *sharedusers.Service

	// repositories
	userRepo *repository.Users
}

func NewService(
	deps clerk.Deps,
	dapiSDKClientConfig *sdk.ClientConfig,
	newSDKConfig sdkutils.ConfigConstructor,
) *Service {
	return &Service{
		userClient:          user.NewClient(dapiSDKClientConfig),
		db:                  deps.DB(),
		newSDKConfig:        newSDKConfig,
		serializableService: serializable.NewService(deps.Clock()),
		sharedUserService:   sharedusers.NewService(deps),
		userRepo:            repository.NewUsers(),
	}
}

// CheckUserInInstance checks whether the given user is in the current instance and returns an error if it isn't
func (s *Service) CheckUserInInstance(ctx context.Context, instanceID, userID string) apierror.Error {
	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, userID, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	} else if user == nil {
		return apierror.UserNotFound(userID)
	}

	return nil
}

func (s *Service) Create(ctx context.Context, instanceID string, params user.CreateParams) (*sdk.User, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	userResponse, err := sdkClient.Create(ctx, &params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return userResponse, nil
}

type listParams struct {
	orderBy         string
	query           string
	organizationIDs []string
}

var validUsersOrderByFields = set.New(
	sqbmodel.UserColumns.CreatedAt,
	sqbmodel.UserColumns.UpdatedAt,
	sqbmodel.UserColumns.LastSignInAt,
	repository.OrderFieldEmailAddress,
	repository.OrderFieldFirstName,
	repository.OrderFieldLastName,
	repository.OrderFieldUsername,
	repository.OrderFieldPhoneNumber,
)

func (l listParams) toUserMods() (repository.UsersFindAllModifiers, apierror.Error) {
	var mods repository.UsersFindAllModifiers

	mods.OrganizationIDs = repository.NewParamsWithExclusion(l.organizationIDs...)
	mods.Query = l.query

	if l.orderBy != "" {
		orderByField, err := repository.ConvertToOrderByField(l.orderBy, validUsersOrderByFields)
		if err != nil {
			return mods, err
		}
		mods.OrderBy = &orderByField
	}

	return mods, nil
}

func (l listParams) toSDKListUsersParams() user.ListParams {
	var params user.ListParams
	if l.query != "" {
		params.Query = &l.query
	}
	return params
}

func (s *Service) List(ctx context.Context, instanceID string, params listParams, pagination pagination.Params) ([]*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	mods, apierr := params.toUserMods()
	if apierr != nil {
		return nil, apierr
	}

	users, err := s.userRepo.FindAllWithModifiers(ctx, s.db, instanceID, mods, pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userSerializables, err := s.serializableService.ConvertUsers(ctx, s.db, userSettings, users)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]*serialize.UserResponse, len(users))
	for i, userSerializable := range userSerializables {
		responses[i] = serialize.UserToDashboardAPI(ctx, userSerializable)
	}

	return responses, nil
}

// Count returns the total user count given the supplied filter params.
func (s *Service) Count(ctx context.Context, instanceID string, params listParams) (*user.TotalCount, apierror.Error) {
	sdkParams := params.toSDKListUsersParams()

	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() (*user.TotalCount, apierror.Error) {
		userCount, err := sdkClient.Count(ctx, &sdkParams)
		return userCount, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) Read(ctx context.Context, instanceID, userID string) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	user, err := s.userRepo.FindByIDAndInstance(ctx, s.db, userID, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userSerializable, err := s.serializableService.ConvertUser(ctx, s.db, userSettings, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.UserToDashboardAPI(ctx, userSerializable), nil
}

func (s *Service) Delete(ctx context.Context, instanceID string, userID string) apierror.Error {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return apiErr
	}

	_, err := sdkClient.Delete(ctx, userID)
	return sdkutils.ToAPIError(err)
}

type updateParams struct {
	FirstName                 clerkjson.String `json:"first_name"`
	LastName                  clerkjson.String `json:"last_name"`
	Username                  clerkjson.String `json:"username"`
	Password                  *string          `json:"password"`
	PrimaryEmailAddressID     *string          `json:"primary_email_address_id"`
	PrimaryPhoneNumberID      *string          `json:"primary_phone_number_id"`
	PublicMetadata            json.RawMessage  `json:"public_metadata"`
	PrivateMetadata           json.RawMessage  `json:"private_metadata"`
	UnsafeMetadata            json.RawMessage  `json:"unsafe_metadata"`
	ProfileImageID            *string          `json:"profile_image_id"`
	SkipPasswordChecks        *bool            `json:"skip_password_checks"`
	SignOutOfOtherSessions    *bool            `json:"sign_out_of_other_sessions"`
	DeleteSelfEnabled         *bool            `json:"delete_self_enabled"`
	CreateOrganizationEnabled *bool            `json:"create_organization_enabled"`
}

func (p updateParams) toUpdateForm() *sharedusers.UpdateForm {
	updateForm := &sharedusers.UpdateForm{
		FirstName:                 p.FirstName,
		LastName:                  p.LastName,
		Username:                  p.Username,
		Password:                  p.Password,
		PrimaryEmailAddressID:     p.PrimaryEmailAddressID,
		PrimaryPhoneNumberID:      p.PrimaryPhoneNumberID,
		ProfileImageID:            p.ProfileImageID,
		SkipPasswordChecks:        p.SkipPasswordChecks != nil && *p.SkipPasswordChecks,
		SignOutOfOtherSessions:    p.SignOutOfOtherSessions != nil && *p.SignOutOfOtherSessions,
		DeleteSelfEnabled:         p.DeleteSelfEnabled,
		CreateOrganizationEnabled: p.CreateOrganizationEnabled,
	}

	if p.PublicMetadata != nil {
		updateForm.PublicMetadata = &p.PublicMetadata
	}

	if p.PrivateMetadata != nil {
		updateForm.PrivateMetadata = &p.PrivateMetadata
	}

	if p.UnsafeMetadata != nil {
		updateForm.UnsafeMetadata = &p.UnsafeMetadata
	}

	return updateForm
}

func (s Service) Update(ctx context.Context, params *updateParams, userID string) apierror.Error {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	_, apiErr := s.sharedUserService.Update(ctx, env, userID, params.toUpdateForm(), env.Instance, userSettings)
	if apiErr != nil {
		return apiErr
	}

	return nil
}

func (s *Service) SetPreferences(ctx context.Context, preferences string) apierror.Error {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	_, err := s.userClient.Update(ctx, activeSession.Subject, &user.UpdateParams{
		PublicMetadata: sdk.JSONRawMessage(json.RawMessage(preferences)),
	})
	return sdkutils.ToAPIError(err)
}

type ImpersonateURLParams struct {
	UserID string
	Host   string
}

type ImpersonateResponse struct {
	Location string `json:"location"`
}

func (s *Service) ImpersonateURL(ctx context.Context, params ImpersonateURLParams) (*ImpersonateResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	if !authorization.HasAccess(activeSession.ActiveOrganizationRole, billing.Features.Impersonation) {
		return nil, apierror.InvalidAuthorization()
	}

	actor, err := json.Marshal(map[string]string{
		"iss": params.Host,
		"sid": activeSession.SessionID,
		"sub": activeSession.Subject,
	})
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	sdkConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, env.Instance.ID)
	if apiErr != nil {
		return nil, apiErr
	}
	actorTokenResponse, err := actortoken.NewClient(sdkConfig).Create(ctx, &actortoken.CreateParams{
		UserID:           sdk.String(params.UserID),
		Actor:            actor,
		ExpiresInSeconds: sdk.Int64(int64(constants.ExpiryTimeShort)),
	})
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}

	fapiURL := env.Domain.FapiURL()
	fapiRedirectURL, err := url.Parse(fapiURL)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	fapiRedirectURL = fapiRedirectURL.JoinPath("/v1/tickets/accept")
	query := fapiRedirectURL.Query()
	query.Set("ticket", actorTokenResponse.Token)
	if env.DisplayConfig.Experimental.AfterImpersonateRedirectURL != "" {
		query.Set("redirect_url", env.DisplayConfig.Experimental.AfterImpersonateRedirectURL)
	}

	fapiRedirectURL.RawQuery = query.Encode()

	return &ImpersonateResponse{
		Location: fapiRedirectURL.String(),
	}, nil
}

func (s *Service) Ban(ctx context.Context, instanceID string, userID string) (*sdk.User, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	user, err := sdkClient.Ban(ctx, userID)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return user, nil
}

func (s *Service) Unban(ctx context.Context, instanceID string, userID string) (*sdk.User, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	user, err := sdkClient.Unban(ctx, userID)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return user, nil
}

func (s *Service) Unlock(ctx context.Context, instanceID string, userID string) (*sdk.User, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	user, err := sdkClient.Unlock(ctx, userID)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return user, nil
}

type ListOrganizationMembershipsParams struct {
	InstanceID string
	UserID     string
	Pagination pagination.Params
}

func (s *Service) ListOrganizationMemberships(ctx context.Context, params ListOrganizationMembershipsParams) (*sdk.OrganizationMembershipList, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, params.InstanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	sdkParams := &user.ListOrganizationMembershipsParams{}
	sdkParams.Offset = sdk.Int64(int64(params.Pagination.Offset))
	sdkParams.Limit = sdk.Int64(int64(params.Pagination.Limit))

	return sdkutils.WithRetry(func() (*sdk.OrganizationMembershipList, apierror.Error) {
		membershipsResponse, err := sdkClient.ListOrganizationMemberships(ctx, params.UserID, sdkParams)
		return membershipsResponse, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

func (s *Service) newSDKClientForInstance(ctx context.Context, instanceID string) (*user.Client, apierror.Error) {
	config, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return user.NewClient(config), nil
}
