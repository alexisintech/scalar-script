package organizations

import (
	"context"
	"encoding/json"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/metadata"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/validate"

	"github.com/go-playground/validator/v10"
	"github.com/volatiletech/sqlboiler/v4/types"
)

type Service struct {
	db        database.Database
	validator *validator.Validate

	// services
	eventsService        *events.Service
	organizationsService *organizations.Service
	orgLogosService      *organizations.LogosService

	// repositories
	organizationsRepo *repository.Organization
	usersRepo         *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                   deps.DB(),
		validator:            validator.New(),
		eventsService:        events.NewService(deps),
		organizationsService: organizations.NewService(deps),
		orgLogosService:      organizations.NewLogosService(deps),
		organizationsRepo:    repository.NewOrganization(),
		usersRepo:            repository.NewUsers(),
	}
}

// EnsureOrganizationExists checks whether the given organization
// exists in the current instance
func (s *Service) EnsureOrganizationExists(ctx context.Context, organizationID string) apierror.Error {
	env := environment.FromContext(ctx)
	exists, err := s.organizationsRepo.ExistsByIDAndInstance(ctx, s.db, organizationID, env.Instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	} else if !exists {
		return apierror.ResourceNotFound()
	}
	return nil
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

type ListParams struct {
	IncludeMembersCount bool
	Query               string   `validate:"omitempty"`
	UserIDs             []string `validate:"omitempty"`
	orderBy             *string
}

func (params *ListParams) validate() apierror.Error {
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

var validOrganizationOrderByFields = set.New(
	sqbmodel.OrganizationColumns.CreatedAt,
	sqbmodel.OrganizationColumns.Name,
	"members_count",
)

func (params *ListParams) toOrganizationsMods() (repository.OrganizationsFindAllModifiers, apierror.Error) {
	var mods repository.OrganizationsFindAllModifiers
	if params.Query != "" {
		mods.Query = params.Query
	}

	if params.orderBy != nil && *params.orderBy != "" {
		orderByField, err := repository.ConvertToOrderByField(*params.orderBy, validOrganizationOrderByFields)
		if err != nil {
			return mods, err
		}
		mods.OrderBy = orderByField
	}

	mods.UserIDs = repository.NewParamsWithExclusion(params.UserIDs...)
	return mods, nil
}

func (s *Service) List(ctx context.Context, params ListParams, paginationParams pagination.Params) (*serialize.PaginatedResponse, apierror.Error) {
	// Validate parameters
	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	// Extract organizations modifiers
	findAllParams, apiErr := params.toOrganizationsMods()
	if apiErr != nil {
		return nil, apiErr
	}

	// Retrieve organizations
	env := environment.FromContext(ctx)
	orgsWithMembers, err := s.organizationsRepo.FindAllByInstanceWithMembersCount(ctx, s.db, env.Instance.ID, findAllParams, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Retrieve organization count
	totalCount, err := s.organizationsRepo.CountByInstanceWithModifiers(ctx, s.db, env.Instance.ID, findAllParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Serialize results
	data := make([]interface{}, len(orgsWithMembers))
	for i, orgWithMembers := range orgsWithMembers {
		options := make([]func(response *serialize.OrganizationResponse), 0)
		if params.IncludeMembersCount {
			options = append(options, serialize.WithMembersCount(orgWithMembers.MembersCount))
		}
		data[i] = serialize.OrganizationBAPI(ctx, &orgWithMembers.Organization, options...)
	}

	return serialize.Paginated(data, totalCount), nil
}

type CreateParams struct {
	Name                  string           `json:"name" form:"name" validate:"required,max=256"`
	Slug                  *string          `json:"slug" form:"slug"`
	CreatedBy             string           `json:"created_by" form:"created_by" validate:"required"`
	MaxAllowedMemberships *int             `json:"max_allowed_memberships" form:"max_allowed_memberships" validate:"omitempty,numeric,gte=0"`
	PublicMetadata        *json.RawMessage `json:"public_metadata" form:"public_metadata"`
	PrivateMetadata       *json.RawMessage `json:"private_metadata" form:"private_metadata"`
}

func (p CreateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	if p.Slug != nil {
		if err := validate.OrganizationSlugFormat(*p.Slug, "slug"); err != nil {
			return err
		}
	}
	return metadata.Validate(p.toMetadata())
}

func (p CreateParams) toMetadata() metadata.Metadata {
	v := metadata.Metadata{}
	if p.PrivateMetadata != nil {
		v.Private = *p.PrivateMetadata
	}
	if p.PublicMetadata != nil {
		v.Public = *p.PublicMetadata
	}
	return v
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.OrganizationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	user, err := s.usersRepo.QueryByIDAndInstance(ctx, s.db, params.CreatedBy, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.OrganizationCreatorNotFound(params.CreatedBy)
	}

	metadataParams := params.toMetadata()
	organization := &model.Organization{
		Organization: &sqbmodel.Organization{
			InstanceID:            env.Instance.ID,
			Name:                  params.Name,
			CreatedBy:             params.CreatedBy,
			MaxAllowedMemberships: env.AuthConfig.OrganizationSettings.MaxAllowedMemberships,
			PublicMetadata:        types.JSON(metadataParams.Public),
			PrivateMetadata:       types.JSON(metadataParams.Private),
		},
	}

	if params.MaxAllowedMemberships != nil {
		organization.MaxAllowedMemberships = *params.MaxAllowedMemberships
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

	return serialize.OrganizationBAPI(ctx, organization), nil
}

// Read returns a serialized organization whose ID or slug matches the
// idOrSlug parameter.
func (s *Service) Read(ctx context.Context, idOrSlug string) (*serialize.OrganizationResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	var org *model.Organization
	org, err := s.organizationsRepo.QueryByIDOrSlugAndInstance(ctx, s.db, idOrSlug, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if org == nil {
		return nil, apierror.ResourceNotFound()
	}
	return serialize.OrganizationBAPI(ctx, org), nil
}

type UpdateParams struct {
	Name                  *string `json:"name" form:"name"`
	Slug                  *string `json:"slug" form:"slug"`
	MaxAllowedMemberships *int    `json:"max_allowed_memberships" form:"max_allowed_memberships"`
	AdminDeleteEnabled    *bool   `json:"admin_delete_enabled" form:"admin_delete_enabled"`
	OrganizationID        string
	PublicMetadata        *json.RawMessage `json:"public_metadata" form:"public_metadata"`
	PrivateMetadata       *json.RawMessage `json:"private_metadata" form:"private_metadata"`
}

func (s *Service) Update(ctx context.Context, params UpdateParams) (*serialize.OrganizationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	var organization *model.Organization
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		organization, err = s.organizationsService.Update(ctx, tx, organizations.UpdateParams{
			Name:                  params.Name,
			Slug:                  params.Slug,
			MaxAllowedMemberships: params.MaxAllowedMemberships,
			AdminDeleteEnabled:    params.AdminDeleteEnabled,
			OrganizationID:        params.OrganizationID,
			PublicMetadata:        params.PublicMetadata,
			PrivateMetadata:       params.PrivateMetadata,
			Instance:              env.Instance,
			Subscription:          env.Subscription,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return serialize.OrganizationBAPI(ctx, organization), nil
}

type DeleteParams struct {
	OrganizationID string
}

func (s *Service) Delete(ctx context.Context, params DeleteParams) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	// Ensure organization exists
	org, err := s.organizationsRepo.QueryByIDAndInstance(ctx, s.db, params.OrganizationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if org == nil {
		return nil, apierror.ResourceNotFound()
	}

	var response *serialize.DeletedObjectResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		response, err = s.organizationsService.Delete(ctx, tx, organizations.DeleteParams{
			Organization: org,
			Env:          env,
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

func (s *Service) UpdateLogo(
	ctx context.Context,
	params organizations.UpdateLogoParams,
	instance *model.Instance,
) (*serialize.OrganizationResponse, apierror.Error) {
	var org *model.Organization

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

	return serialize.OrganizationBAPI(ctx, org), nil
}

func (s *Service) DeleteLogo(ctx context.Context, organizationID string) (*serialize.OrganizationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	org, err := s.organizationsRepo.QueryByIDAndInstance(ctx, s.db, organizationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if org == nil {
		return nil, apierror.OrganizationNotFound()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		org, err = s.orgLogosService.Delete(ctx, tx, organizations.DeleteLogoParams{
			Organization: org,
			Instance:     env.Instance,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationBAPI(ctx, org), nil
}

type UpdateMetadataParams struct {
	PublicMetadata  json.RawMessage `json:"public_metadata" form:"public_metadata"`
	PrivateMetadata json.RawMessage `json:"private_metadata" form:"private_metadata"`
	OrganizationID  string          `json:"-"`
}

func (s *Service) UpdateMetadata(ctx context.Context, params UpdateMetadataParams) (*serialize.OrganizationResponse, apierror.Error) {
	org, err := s.organizationsRepo.FindByID(ctx, s.db, params.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	merged, mergeErr := metadata.Merge(org.Metadata(), metadata.Metadata{
		Public:  params.PublicMetadata,
		Private: params.PrivateMetadata,
	})
	if mergeErr != nil {
		return nil, mergeErr
	}

	env := environment.FromContext(ctx)
	var updatedOrg *model.Organization
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		updatedOrg, err = s.organizationsService.Update(ctx, tx, organizations.UpdateParams{
			OrganizationID:  params.OrganizationID,
			PublicMetadata:  &merged.Public,
			PrivateMetadata: &merged.Private,
			Instance:        env.Instance,
			Subscription:    env.Subscription,
		})
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationBAPI(ctx, updatedOrg), nil
}
