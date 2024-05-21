package organization_permissions

import (
	"context"
	"regexp"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-playground/validator/v10"
)

var (
	allowedKeyCharacters = regexp.MustCompile("^org:[a-z0-9_]+:[a-z0-9_]+$").MatchString
)

type Service struct {
	db        database.Database
	validator *validator.Validate

	// services
	eventsService *events.Service

	// repositories
	permissionRepo        *repository.Permission
	subscriptionPlansRepo *repository.SubscriptionPlans
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                    deps.DB(),
		validator:             validator.New(),
		eventsService:         events.NewService(deps),
		permissionRepo:        repository.NewPermission(),
		subscriptionPlansRepo: repository.NewSubscriptionPlans(),
	}
}

type ListParams struct {
	pagination.Params
	Query   *string
	OrderBy *string
}

func (p ListParams) toMods() (repository.PermissionFindAllModifiers, apierror.Error) {
	validOrderByFields := set.New(
		sqbmodel.PermissionColumns.CreatedAt,
		sqbmodel.PermissionColumns.Name,
		sqbmodel.PermissionColumns.Key,
	)

	mods := repository.PermissionFindAllModifiers{
		Query: p.Query,
	}

	if p.OrderBy != nil {
		orderByField, err := repository.ConvertToOrderByField(*p.OrderBy, validOrderByFields)
		if err != nil {
			return mods, err
		}

		mods.OrderBy = &orderByField
	}

	return mods, nil
}

func (s *Service) List(ctx context.Context, instanceID string, params ListParams) (*serialize.PaginatedResponse, apierror.Error) {
	mods, apiErr := params.toMods()
	if apiErr != nil {
		return nil, apiErr
	}

	orgPermissions, err := s.permissionRepo.FindAllByInstanceWithModifiers(ctx, s.db, instanceID, mods, params.Params)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.permissionRepo.CountByInstanceWithModifiers(ctx, s.db, instanceID, mods)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]any, len(orgPermissions))
	for i, orgPermission := range orgPermissions {
		responses[i] = serialize.Permission(orgPermission)
	}

	return serialize.Paginated(responses, totalCount), nil
}

type CreateParams struct {
	Name        string `json:"name" validate:"required"`
	Key         string `json:"key" validate:"required"`
	Description string `json:"description" validate:"required"`
}

func (params *CreateParams) validate(validator *validator.Validate) apierror.Error {
	params.Key = strings.ToLower(params.Key)

	if err := validator.Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}

	if strings.HasPrefix(params.Key, constants.OrgSystemPermissionPrefix) {
		return apierror.FormInvalidParameterFormat(
			param.Key.Name,
			`Key cannot begin with "org:sys_" because it is reserved for system permissions`,
		)
	}

	if !allowedKeyCharacters(params.Key) {
		return apierror.FormInvalidParameterFormat("key", `Must have the format "org:<segment1>:<segment2>" where each segment consists of one or more lowercase letters, digits, or underscores`)
	}

	return nil
}

func (s *Service) Create(ctx context.Context, instanceID string, params CreateParams) (*serialize.PermissionResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	if !env.Instance.HasAccessToAllFeatures() {
		plans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		unsupportedFeatures := billing.ValidateSupportedFeatures(
			billing.CustomOrganizationPermissionsFeatures(env.AuthConfig.OrganizationSettings, env.Instance.CreatedAt),
			env.Subscription,
			plans...,
		)
		if len(unsupportedFeatures) > 0 {
			return nil, apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
		}
	}

	// check that the number of permissions is less than the max
	numPerms, err := s.permissionRepo.CountByInstance(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if int(numPerms) == env.AuthConfig.OrganizationSettings.MaxAllowedPermissions {
		return nil, apierror.OrganizationInstancePermissionsQuotaExceeded(env.AuthConfig.OrganizationSettings.MaxAllowedPermissions)
	}

	orgPermission := &model.Permission{Permission: &sqbmodel.Permission{
		InstanceID:  instanceID,
		Name:        params.Name,
		Key:         params.Key,
		Description: params.Description,
		Type:        string(constants.RTUser),
	}}

	var response *serialize.PermissionResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err := s.permissionRepo.Insert(ctx, tx, orgPermission); err != nil {
			return true, err
		}

		response = serialize.Permission(orgPermission)

		if err := s.eventsService.PermissionCreated(ctx, tx, env.Instance, response); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniquePermissionKey) {
			return nil, apierror.FormIdentifierExists("key")
		}
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) Read(ctx context.Context, instanceID, permissionID string) (*serialize.PermissionResponse, apierror.Error) {
	orgPermission, err := s.permissionRepo.QueryByIDAndInstance(ctx, s.db, permissionID, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgPermission == nil {
		return nil, apierror.ResourceNotFound()
	}

	return serialize.Permission(orgPermission), nil
}

type UpdateParams struct {
	Name        *string `json:"name"`
	Key         *string `json:"key"`
	Description *string `json:"description"`
}

func (params *UpdateParams) validate() apierror.Error {
	if params.Name != nil && *params.Name == "" {
		return apierror.FormMissingParameter("name")
	}

	if params.Key != nil {
		sanitizedKey := strings.ToLower(*params.Key)
		params.Key = &sanitizedKey

		if strings.HasPrefix(*params.Key, constants.OrgSystemPermissionPrefix) {
			return apierror.FormInvalidParameterFormat(
				param.Key.Name,
				`Key cannot begin with "org:sys_" because it is reserved for system permissions`,
			)
		}

		if !allowedKeyCharacters(*params.Key) {
			return apierror.FormInvalidParameterFormat("key", `Must have the format "org:<segment1>:<segment2>" where each segment consists of one or more lowercase letters, digits, or underscores`)
		}
	}

	if params.Description != nil && *params.Description == "" {
		return apierror.FormMissingParameter("description")
	}

	return nil
}

func (s *Service) Update(ctx context.Context, instanceID, permissionID string, params UpdateParams) (*serialize.PermissionResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(); apiErr != nil {
		return nil, apiErr
	}

	orgPermission, err := s.permissionRepo.QueryByIDAndInstance(ctx, s.db, permissionID, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgPermission == nil {
		return nil, apierror.ResourceNotFound()
	}
	if orgPermission.Type == string(constants.RTSystem) {
		return nil, apierror.OrganizationSystemPermissionNotModifiable()
	}

	columnsToUpdate := updateAndFindColumns(orgPermission, params)

	var response *serialize.PermissionResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err = s.permissionRepo.Update(ctx, tx, orgPermission, columnsToUpdate...); err != nil {
			return true, err
		}

		response = serialize.Permission(orgPermission)

		if err := s.eventsService.PermissionUpdated(ctx, tx, env.Instance, response); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniquePermissionKey) {
			return nil, apierror.FormIdentifierExists("key")
		}
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) Delete(ctx context.Context, instanceID, permissionID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	orgPermission, err := s.permissionRepo.QueryByIDAndInstance(ctx, s.db, permissionID, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgPermission == nil {
		return nil, apierror.ResourceNotFound()
	}
	if orgPermission.Type == string(constants.RTSystem) {
		return nil, apierror.OrganizationSystemPermissionNotModifiable()
	}

	var response *serialize.DeletedObjectResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if _, err := s.permissionRepo.DeleteByIDAndInstance(ctx, tx, permissionID, instanceID); err != nil {
			return true, err
		}

		response = serialize.DeletedObject(permissionID, serialize.PermissionObjectName)

		if err := s.eventsService.PermissionDeleted(ctx, tx, env.Instance, response); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func updateAndFindColumns(orgPermission *model.Permission, params UpdateParams) []string {
	columnsToUpdate := make([]string, 0)

	if params.Name != nil {
		orgPermission.Name = *params.Name
		columnsToUpdate = append(columnsToUpdate, sqbmodel.PermissionColumns.Name)
	}
	if params.Key != nil {
		orgPermission.Key = *params.Key
		columnsToUpdate = append(columnsToUpdate, sqbmodel.PermissionColumns.Key)
	}
	if params.Description != nil {
		orgPermission.Description = *params.Description
		columnsToUpdate = append(columnsToUpdate, sqbmodel.PermissionColumns.Description)
	}

	return columnsToUpdate
}
