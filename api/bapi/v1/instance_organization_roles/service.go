package instance_organization_roles

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/pagination"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	roleRepo *repository.Role
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:       deps.DB(),
		roleRepo: repository.NewRole(),
	}
}

type ListParams struct {
	pagination pagination.Params
	query      *string
	orderBy    *string
}

func (p ListParams) toMods() (repository.RoleFindAllModifiers, apierror.Error) {
	validOrderByFields := set.New(
		sqbmodel.RoleColumns.CreatedAt,
		sqbmodel.RoleColumns.Name,
		sqbmodel.RoleColumns.Key,
	)

	mods := repository.RoleFindAllModifiers{
		Query: p.query,
	}

	if p.orderBy != nil {
		orderByField, err := repository.ConvertToOrderByField(*p.orderBy, validOrderByFields)
		if err != nil {
			return mods, err
		}

		mods.OrderBy = &orderByField
	}

	return mods, nil
}

func (s *Service) List(ctx context.Context, params ListParams) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	mods, apiErr := params.toMods()
	if apiErr != nil {
		return nil, apiErr
	}

	orgRoles, err := s.roleRepo.FindAllByInstanceWithPermissionsWithModifiers(ctx, s.db, env.Instance.ID, mods, params.pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.roleRepo.CountByInstanceWithModifiers(ctx, s.db, env.Instance.ID, mods)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]any, len(orgRoles))
	for i, orgRole := range orgRoles {
		responses[i] = serialize.Role(orgRole.Role, orgRole.Permissions)
	}

	return serialize.Paginated(responses, totalCount), nil
}
