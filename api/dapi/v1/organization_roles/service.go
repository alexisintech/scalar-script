package organization_roles

import (
	"context"
	"regexp"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/api/shared/serializable"
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

	"github.com/go-playground/validator/v10"
	"github.com/vgarvardt/gue/v2"
)

var (
	allowedKeyCharacters = regexp.MustCompile("^org:[a-z0-9_]+$").MatchString
)

type Service struct {
	db        database.Database
	gueClient *gue.Client
	validator *validator.Validate

	// services
	eventsService        *events.Service
	serializableService  *serializable.Service
	organizationsService *organizations.Service

	// repositories
	authConfigRepo        *repository.AuthConfig
	orgInvitationRepo     *repository.OrganizationInvitation
	orgMemberRepo         *repository.OrganizationMembership
	permissionRepo        *repository.Permission
	roleRepo              *repository.Role
	rolePermissionRepo    *repository.RolePermission
	subscriptionPlansRepo *repository.SubscriptionPlans
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                    deps.DB(),
		gueClient:             deps.GueClient(),
		validator:             validator.New(),
		eventsService:         events.NewService(deps),
		serializableService:   serializable.NewService(deps.Clock()),
		organizationsService:  organizations.NewService(deps),
		authConfigRepo:        repository.NewAuthConfig(),
		orgInvitationRepo:     repository.NewOrganizationInvitation(),
		orgMemberRepo:         repository.NewOrganizationMembership(),
		permissionRepo:        repository.NewPermission(),
		roleRepo:              repository.NewRole(),
		rolePermissionRepo:    repository.NewRolePermission(),
		subscriptionPlansRepo: repository.NewSubscriptionPlans(),
	}
}

type ListParams struct {
	pagination.Params
	Query   *string
	OrderBy *string
}

func (p ListParams) toMods() (repository.RoleFindAllModifiers, apierror.Error) {
	validOrderByFields := set.New(
		sqbmodel.RoleColumns.CreatedAt,
		sqbmodel.RoleColumns.Name,
		sqbmodel.RoleColumns.Key,
	)

	mods := repository.RoleFindAllModifiers{
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

	orgRoles, err := s.roleRepo.FindAllByInstanceWithPermissionsWithModifiers(ctx, s.db, instanceID, mods, params.Params)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.roleRepo.CountByInstanceWithModifiers(ctx, s.db, instanceID, mods)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]any, len(orgRoles))
	for i, orgRole := range orgRoles {
		responses[i] = serialize.Role(orgRole.Role, orgRole.Permissions)
	}

	return serialize.Paginated(responses, totalCount), nil
}

type CreateParams struct {
	Name        string   `json:"name" validate:"required"`
	Key         string   `json:"key" validate:"required"`
	Description string   `json:"description" validate:"required"`
	Permissions []string `json:"permissions"`
}

func (params *CreateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}

	if !allowedKeyCharacters(params.Key) {
		return apierror.FormInvalidParameterFormat("key", `Must start with "org:" followed by one or more lowercase letters, digits or underscores`)
	}

	return ensureUniquePermissions(params.Permissions)
}

func (s *Service) Create(ctx context.Context, instanceID string, params CreateParams) (*serialize.RoleResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := s.validateCreateParams(ctx, params, instanceID); apiErr != nil {
		return nil, apiErr
	}

	if !env.Instance.HasAccessToAllFeatures() {
		plans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		unsupportedFeatures := billing.ValidateSupportedFeatures(
			billing.CustomOrganizationRolesFeatures(env.AuthConfig.OrganizationSettings, env.Instance.CreatedAt),
			env.Subscription,
			plans...,
		)
		if len(unsupportedFeatures) > 0 {
			return nil, apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
		}
	}

	// check that the number of roles is less than the max
	numRoles, err := s.roleRepo.CountByInstance(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if int(numRoles) == env.AuthConfig.OrganizationSettings.MaxAllowedRoles {
		return nil, apierror.OrganizationInstanceRolesQuotaExceeded(env.AuthConfig.OrganizationSettings.MaxAllowedRoles)
	}

	orgRole := &model.Role{Role: &sqbmodel.Role{
		InstanceID:  env.Instance.ID,
		Name:        params.Name,
		Key:         params.Key,
		Description: params.Description,
	}}

	var response *serialize.RoleResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err := s.roleRepo.Insert(ctx, tx, orgRole); err != nil {
			return true, err
		}

		if err := s.associateRolePermissions(ctx, tx, params.Permissions, env.Instance.ID, orgRole.ID); err != nil {
			return true, err
		}

		roleSerializable, err := s.serializableService.ConvertOrganizationRole(ctx, tx, orgRole)
		if err != nil {
			return true, err
		}

		response = serialize.Role(roleSerializable.Role, roleSerializable.Permissions)

		if err := s.eventsService.RoleCreated(ctx, tx, env.Instance, response); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueRoleKey) {
			return nil, apierror.FormIdentifierExists("key")
		}
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) Read(ctx context.Context, instanceID, roleID string) (*serialize.RoleResponse, apierror.Error) {
	orgRole, err := s.roleRepo.QueryByIDAndInstance(ctx, s.db, roleID, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgRole == nil {
		return nil, apierror.ResourceNotFound()
	}

	roleSerializable, err := s.serializableService.ConvertOrganizationRole(ctx, s.db, orgRole)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Role(roleSerializable.Role, roleSerializable.Permissions), nil
}

type UpdateParams struct {
	Name        *string   `json:"name"`
	Key         *string   `json:"key"`
	Description *string   `json:"description"`
	Permissions *[]string `json:"permissions"`
}

func (params UpdateParams) validate() apierror.Error {
	if params.Name != nil && *params.Name == "" {
		return apierror.FormMissingParameter("name")
	}

	if params.Key != nil && !allowedKeyCharacters(*params.Key) {
		return apierror.FormInvalidParameterFormat("key", `Must start with "org:" followed by one or more lowercase letters, digits or underscores`)
	}

	if params.Description != nil && *params.Description == "" {
		return apierror.FormMissingParameter("description")
	}

	if params.Permissions != nil {
		return ensureUniquePermissions(*params.Permissions)
	}

	return nil
}

func (s *Service) Update(ctx context.Context, instanceID, roleID string, params UpdateParams) (*serialize.RoleResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := s.validateUpdateParams(ctx, params, instanceID); apiErr != nil {
		return nil, apiErr
	}

	orgRole, err := s.roleRepo.QueryByIDAndInstance(ctx, s.db, roleID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgRole == nil {
		return nil, apierror.ResourceNotFound()
	}

	orgRoleKeyBefore := orgRole.Key
	isCreatorRole := env.AuthConfig.IsOrganizationCreatorRole(orgRoleKeyBefore)

	// if the role is used as the default org creator role,
	// make sure the role still has all minimum required permissions
	if isCreatorRole && params.Permissions != nil {
		permissions, err := s.permissionRepo.FindAllByInstanceAndIDs(ctx, s.db, env.Instance.ID, *params.Permissions)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		if err := s.organizationsService.EnsureMinimumSystemPermissions(permissions); err != nil {
			return nil, err
		}
	}

	columnsToUpdate := updateAndFindColumns(orgRole, params)

	var response *serialize.RoleResponse
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		// 1. Update the properties of the role if needed.
		// 2. Delete the existing role permission association.
		// 3. Create the new role permission association based on what the customer provided.
		if len(columnsToUpdate) > 0 {
			if err = s.roleRepo.Update(ctx, txEmitter, orgRole, columnsToUpdate...); err != nil {
				return true, err
			}
		}

		if params.Permissions != nil {
			if _, err := s.rolePermissionRepo.DeleteByRoleID(ctx, txEmitter, orgRole.ID); err != nil {
				return true, err
			}

			if err := s.associateRolePermissions(ctx, txEmitter, *params.Permissions, env.Instance.ID, orgRole.ID); err != nil {
				return true, err
			}
		}

		if orgRoleKeyBefore != orgRole.Key && isCreatorRole {
			// If the role is used as the default creator role, update also instance's organization settings
			env.AuthConfig.OrganizationSettings.CreatorRole = orgRole.Key
			if err := s.authConfigRepo.UpdateOrganizationSettings(ctx, txEmitter, env.AuthConfig); err != nil {
				return true, err
			}
		}

		isDomainDefaultRole := env.AuthConfig.IsOrganizationDomainDefaultRole(orgRoleKeyBefore)
		if orgRoleKeyBefore != orgRole.Key && isDomainDefaultRole {
			// If the role is used as the organization domain default role, update also instance's organization settings
			env.AuthConfig.OrganizationSettings.Domains.DefaultRole = orgRole.Key
			if err := s.authConfigRepo.UpdateOrganizationSettings(ctx, txEmitter, env.AuthConfig); err != nil {
				return true, err
			}
		}

		roleSerializable, err := s.serializableService.ConvertOrganizationRole(ctx, txEmitter, orgRole)
		if err != nil {
			return true, err
		}

		response = serialize.Role(roleSerializable.Role, roleSerializable.Permissions)

		if err := s.eventsService.RoleUpdated(ctx, txEmitter, env.Instance, response); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueRoleKey) {
			return nil, apierror.FormIdentifierExists("key")
		}
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) Delete(ctx context.Context, orgRoleID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	orgRole, err := s.roleRepo.QueryByIDAndInstance(ctx, s.db, orgRoleID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgRole == nil {
		return nil, apierror.ResourceNotFound()
	}

	if env.AuthConfig.IsOrganizationCreatorRole(orgRole.Key) {
		// Can't delete a role that is used as the default creator role
		return nil, apierror.OrganizationRoleUsedAsCreatorRole()
	}

	if env.AuthConfig.IsOrganizationDomainDefaultRole(orgRole.Key) {
		// Can't delete a role that is used as the organization domain default role
		return nil, apierror.OrganizationRoleUsedAsDomainDefaultRole()
	}

	exists, err := s.orgMemberRepo.ExistsByInstanceAndRole(ctx, s.db, env.Instance.ID, orgRole.Key)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if exists {
		// Can't delete a role that is used from at least one member
		return nil, apierror.OrganizationRoleAssignedToMembers()
	}

	// check if any pending org invitations use this role
	exists, err = s.orgInvitationRepo.ExistsPendingByRoleID(ctx, s.db, orgRole.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if exists {
		return nil, apierror.OrganizationRoleExistsInInvitations()
	}

	var response *serialize.DeletedObjectResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if _, err := s.roleRepo.DeleteByIDAndInstance(ctx, tx, orgRoleID, env.Instance.ID); err != nil {
			return true, err
		}

		response = serialize.DeletedObject(orgRoleID, serialize.RoleObjectName)

		if err := s.eventsService.RoleDeleted(ctx, tx, env.Instance, response); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) AssignPermission(ctx context.Context, roleID, permissionID string) (*serialize.RoleResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	// check that role exists
	orgRole, err := s.roleRepo.QueryByIDAndInstance(ctx, s.db, roleID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgRole == nil {
		return nil, apierror.ResourceNotFound()
	}

	// check that permission exists
	orgPerm, err := s.permissionRepo.QueryByIDAndInstance(ctx, s.db, permissionID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgPerm == nil {
		return nil, apierror.OrganizationPermissionNotFound()
	}

	var response *serialize.RoleResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		rolePerm := &model.RolePermission{
			RolePermission: &sqbmodel.RolePermission{
				InstanceID:   env.Instance.ID,
				RoleID:       roleID,
				PermissionID: permissionID,
			},
		}
		err = s.rolePermissionRepo.Insert(ctx, tx, rolePerm)
		if err != nil {
			return true, err
		}

		roleSerializable, err := s.serializableService.ConvertOrganizationRole(ctx, tx, orgRole)
		if err != nil {
			return true, err
		}

		response = serialize.Role(roleSerializable.Role, roleSerializable.Permissions)

		if err := s.eventsService.RoleUpdated(ctx, tx, env.Instance, response); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueRolePermission) {
			// role permission association already existed
			return nil, apierror.OrganizationRolePermissionAssociationExists()
		}

		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) RemovePermission(ctx context.Context, roleID, permissionID string) (*serialize.RoleResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	// check that role exists
	orgRole, err := s.roleRepo.QueryByIDAndInstance(ctx, s.db, roleID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgRole == nil {
		return nil, apierror.ResourceNotFound()
	}

	// check that permission exists
	orgPerm, err := s.permissionRepo.QueryByIDAndInstance(ctx, s.db, permissionID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgPerm == nil {
		return nil, apierror.OrganizationPermissionNotFound()
	}

	if env.AuthConfig.IsOrganizationCreatorRole(orgRole.Key) && constants.MinRequiredOrgPermissions.Contains(orgPerm.Key) {
		return nil, apierror.OrganizationMissingCreatorRolePermissions(constants.MinRequiredOrgPermissions.Array()...)
	}

	var response *serialize.RoleResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		deletedCount, err := s.rolePermissionRepo.DeleteByRoleAndPermission(ctx, tx, roleID, permissionID)
		if err != nil {
			return true, err
		}

		// role permission didn't exist
		if deletedCount == 0 {
			return false, apierror.OrganizationRolePermissionAssociationNotFound()
		}

		roleSerializable, err := s.serializableService.ConvertOrganizationRole(ctx, tx, orgRole)
		if err != nil {
			return true, err
		}

		response = serialize.Role(roleSerializable.Role, roleSerializable.Permissions)

		if err := s.eventsService.RoleUpdated(ctx, tx, env.Instance, response); err != nil {
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

	return response, nil
}

func (s *Service) validateCreateParams(ctx context.Context, params CreateParams, instanceID string) apierror.Error {
	if apiErr := params.validate(s.validator); apiErr != nil {
		return apiErr
	}

	return s.validatePermissions(ctx, s.db, params.Permissions, instanceID)
}

func (s *Service) validateUpdateParams(ctx context.Context, params UpdateParams, instanceID string) apierror.Error {
	if apiErr := params.validate(); apiErr != nil {
		return apiErr
	}

	if params.Permissions != nil {
		return s.validatePermissions(ctx, s.db, *params.Permissions, instanceID)
	}

	return nil
}

// validatePermissions checks that every permission ID provided exists on the given instance
func (s *Service) validatePermissions(ctx context.Context, exec database.Executor, permissions []string, instanceID string) apierror.Error {
	totalCount, err := s.permissionRepo.CountByInstanceAndIDs(ctx, exec, instanceID, permissions...)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if len(permissions) != totalCount {
		return apierror.OrganizationPermissionNotFound()
	}

	return nil
}

func (s *Service) associateRolePermissions(ctx context.Context, tx database.Tx, permissions []string, instanceID, roleID string) error {
	if len(permissions) == 0 {
		return nil
	}

	rolePerms := make([]*model.RolePermission, len(permissions))
	for i, permissionID := range permissions {
		rolePerms[i] = &model.RolePermission{RolePermission: &sqbmodel.RolePermission{
			InstanceID:   instanceID,
			RoleID:       roleID,
			PermissionID: permissionID,
		}}
	}

	return s.rolePermissionRepo.InsertBulk(ctx, tx, rolePerms)
}

func updateAndFindColumns(orgRole *model.Role, params UpdateParams) []string {
	columnsToUpdate := make([]string, 0)

	if params.Name != nil {
		orgRole.Name = *params.Name
		columnsToUpdate = append(columnsToUpdate, sqbmodel.RoleColumns.Name)
	}
	if params.Key != nil {
		orgRole.Key = *params.Key
		columnsToUpdate = append(columnsToUpdate, sqbmodel.RoleColumns.Key)
	}
	if params.Description != nil {
		orgRole.Description = *params.Description
		columnsToUpdate = append(columnsToUpdate, sqbmodel.RoleColumns.Description)
	}

	return columnsToUpdate
}

func ensureUniquePermissions(permissions []string) apierror.Error {
	if len(permissions) != set.New(permissions...).Count() {
		return apierror.FormDuplicateParameterValue("permissions", strings.Join(permissions, ","))
	}

	return nil
}
