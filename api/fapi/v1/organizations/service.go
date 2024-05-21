package organizations

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_user"
	sentryclerk "clerk/pkg/sentry"

	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/validate"

	"github.com/go-playground/validator/v10"
)

// Service object for organizations related business logic.
type Service struct {
	db database.Database

	// services
	organizationsService *organizations.Service
	orgLogosService      *organizations.LogosService

	// repositories
	imageRepo     *repository.Images
	orgRepo       *repository.Organization
	orgMemberRepo *repository.OrganizationMembership
	rolesRepo     *repository.Role
}

// NewService returns an organizations Service instance.
func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                   deps.DB(),
		organizationsService: organizations.NewService(deps),
		orgLogosService:      organizations.NewLogosService(deps),
		imageRepo:            repository.NewImages(),
		orgRepo:              repository.NewOrganization(),
		orgMemberRepo:        repository.NewOrganizationMembership(),
		rolesRepo:            repository.NewRole(),
	}
}

// Create will attempt to create an organization based on the provided params.
// Checks that the parameters are valid and that the requesting user hasn't
// reached the maximum organization creation limit.
// The organization creator will become an admin of the newly created
// organization.
func (s *Service) Create(ctx context.Context, params *CreateParams) (*serialize.OrganizationResponse, apierror.Error) {
	if !params.CreateOrganizationEnabled {
		return nil, apierror.UserCreateOrgNotEnabled()
	}

	env := environment.FromContext(ctx)

	err := params.validate()
	if err != nil {
		return nil, err
	}

	organization := &model.Organization{
		Organization: &sqbmodel.Organization{
			Name:                  params.Name,
			InstanceID:            params.InstanceID,
			CreatedBy:             params.CreatedBy,
			MaxAllowedMemberships: env.AuthConfig.OrganizationSettings.MaxAllowedMemberships,
		},
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err := s.organizationsService.Create(ctx, tx, organizations.CreateParams{
			Instance:                env.Instance,
			MaxAllowedOrganizations: env.AuthConfig.MaxAllowedOrganizations,
			Organization:            organization,
			Slug:                    params.Slug,
			Subscription:            env.Subscription,
			OrganizationSettings:    env.AuthConfig.OrganizationSettings,
		}); err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.Organization(ctx, organization), nil
}

// CreateParams holds the attributes that can be used to create
// a new organization.
type CreateParams struct {
	Name                      string `validate:"required,max=256"`
	Slug                      *string
	InstanceID                string `validate:"required"`
	CreatedBy                 string `validate:"required"`
	CreateOrganizationEnabled bool
}

// Validate that all required attributes are not blank.
func (params *CreateParams) validate() apierror.Error {
	var formErrs apierror.Error
	if err := validator.New().Struct(params); err != nil {
		formErrs = apierror.Combine(formErrs, apierror.FormValidationFailed(err))
	}
	if params.Slug != nil {
		err := validate.OrganizationSlugFormat(*params.Slug, paramSlug.Name)
		if err != nil {
			formErrs = apierror.Combine(formErrs, err)
		}
	}
	return formErrs
}

func (s *Service) Read(ctx context.Context, organizationID, requestingUserID string) (*serialize.OrganizationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	organization, err := s.orgRepo.QueryByIDAndInstance(ctx, s.db, organizationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if organization == nil {
		return nil, apierror.ResourceNotFound()
	}

	// Ensure the requesting user is part of the organization
	exists, err := s.orgMemberRepo.ExistsByOrganizationAndUser(ctx, s.db, organizationID, requestingUserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if !exists {
		return nil, apierror.NotAMemberInOrganization()
	}

	return serialize.Organization(ctx, organization), nil
}

type UpdateParams struct {
	Name             *string
	Slug             *string
	OrganizationID   string
	RequestingUserID string
}

// Update patches the organization specified by params.OrganizationID
// with the rest of the params. The requesting user must be an admin
// in the organization.
func (s *Service) Update(ctx context.Context, params UpdateParams) (*serialize.OrganizationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionOrgManage, params.RequestingUserID)
	if apiErr != nil {
		return nil, apiErr
	}

	var organization *model.Organization
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		organization, err = s.organizationsService.Update(ctx, tx, organizations.UpdateParams{
			Name:             params.Name,
			Slug:             params.Slug,
			OrganizationID:   params.OrganizationID,
			RequestingUserID: params.RequestingUserID,
			Instance:         env.Instance,
			Subscription:     env.Subscription,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.Organization(ctx, organization), nil
}

type DeleteParams struct {
	OrganizationID   string
	RequestingUserID string
}

// Delete deletes the organization specified by params.OrganizationID.
// The requesting user must be an admin in the organization.
func (s *Service) Delete(ctx context.Context, params DeleteParams) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	// Ensure organization exists
	org, err := s.orgRepo.QueryByIDAndInstance(ctx, s.db, params.OrganizationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if org == nil {
		return nil, apierror.ResourceNotFound()
	}

	// Ensure the org allows admins to delete via FAPI
	if !org.AdminDeleteEnabled {
		return nil, apierror.OrganizationAdminDeleteNotEnabled()
	}

	apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, org.ID, constants.PermissionOrgDelete, params.RequestingUserID)
	if apiErr != nil {
		return nil, apiErr
	}

	var response *serialize.DeletedObjectResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		response, err = s.organizationsService.Delete(ctx, tx, organizations.DeleteParams{
			Organization:     org,
			RequestingUserID: &params.RequestingUserID,
			Env:              env,
		})
		if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

// EnsureOrganizationExists checks whether the given organization
// exists in the current instance
func (s *Service) EnsureOrganizationExists(ctx context.Context, organizationID string) apierror.Error {
	env := environment.FromContext(ctx)
	exists, err := s.orgRepo.ExistsByIDAndInstance(ctx, s.db, organizationID, env.Instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	} else if !exists {
		return apierror.ResourceNotFound()
	}
	return nil
}

func (s *Service) EnsureMembersManagePermission(ctx context.Context, organizationID, userID string) apierror.Error {
	return s.organizationsService.EnsureHasAccess(ctx, s.db, organizationID, constants.PermissionMembersManage, userID)
}

func (s *Service) EmitActiveOrganizationEventIfNeeded(ctx context.Context, organizationID string) {
	env := environment.FromContext(ctx)
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.organizationsService.EmitActiveOrganizationEventIfNeeded(ctx, s.db, organizationID, env.Instance)
		return err != nil, err
	})
	if txErr != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and continue
		sentryclerk.CaptureException(ctx, txErr)
	}
}

func (s *Service) UpdateLogo(
	ctx context.Context,
	params organizations.UpdateLogoParams,
	instance *model.Instance,
) (*serialize.OrganizationResponse, apierror.Error) {
	var org *model.Organization

	apiErr := s.EnsureOrganizationExists(ctx, params.OrganizationID)
	if apiErr != nil {
		return nil, apiErr
	}

	apiErr = s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionOrgManage, params.UploaderUserID)
	if apiErr != nil {
		return nil, apiErr
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		org, err = s.orgLogosService.Update(ctx, tx, params, instance)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.Organization(ctx, org), nil
}

type DeleteLogoParams struct {
	OrganizationID   string
	RequestingUserID string
}

func (s *Service) DeleteLogo(ctx context.Context, params DeleteLogoParams) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionOrgManage, params.RequestingUserID)
	if apiErr != nil {
		return nil, apiErr
	}

	org, err := s.orgRepo.QueryByIDAndInstance(ctx, s.db, params.OrganizationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if org == nil {
		return nil, apierror.ResourceNotFound()
	}
	if !org.LogoPublicURL.Valid {
		return nil, apierror.ImageNotFound()
	}
	logo, err := s.imageRepo.QueryByPublicURL(ctx, s.db, org.LogoPublicURL.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if logo == nil {
		return nil, apierror.ImageNotFound()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		org, apiErr = s.orgLogosService.Delete(ctx, tx, organizations.DeleteLogoParams{
			Organization: org,
			Instance:     env.Instance,
			UserID:       params.RequestingUserID,
		})
		return apiErr != nil, apiErr
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.DeletedObject(logo.ID, serialize.ObjectImage), nil
}

func (s *Service) ListOrganizationRoles(ctx context.Context, orgID string, paginationParams pagination.Params) (*serialize.PaginatedResponse, apierror.Error) {
	user := requesting_user.FromContext(ctx)
	env := environment.FromContext(ctx)
	instanceID := env.Instance.ID

	if apiErr := s.organizationsService.EnsureHasAccessAny(ctx, s.db, orgID, user.ID, constants.PermissionMembersRead, constants.PermissionMembersManage); apiErr != nil {
		return nil, apiErr
	}

	rolesWithPermissions, err := s.rolesRepo.FindAllByInstanceWithPermissions(ctx, s.db, instanceID, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.rolesRepo.CountByInstance(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]interface{}, len(rolesWithPermissions))
	for i, roleWithPerm := range rolesWithPermissions {
		response[i] = serialize.Role(roleWithPerm.Role, roleWithPerm.Permissions)
	}

	return serialize.Paginated(response, totalCount), nil
}
