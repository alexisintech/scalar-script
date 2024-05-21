package instance_organization_permissions

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

	permissionRepo *repository.Permission
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:             deps.DB(),
		permissionRepo: repository.NewPermission(),
	}
}

type ListParams struct {
	pagination pagination.Params
	query      *string
	orderBy    *string
}

func (p ListParams) toMods() (repository.PermissionFindAllModifiers, apierror.Error) {
	validOrderByFields := set.New(
		sqbmodel.PermissionColumns.CreatedAt,
		sqbmodel.PermissionColumns.Name,
		sqbmodel.PermissionColumns.Key,
	)

	mods := repository.PermissionFindAllModifiers{
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

	orgPermissions, err := s.permissionRepo.FindAllByInstanceWithModifiers(ctx, s.db, env.Instance.ID, mods, params.pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.permissionRepo.CountByInstanceWithModifiers(ctx, s.db, env.Instance.ID, mods)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]any, len(orgPermissions))
	for i, orgPermission := range orgPermissions {
		responses[i] = serialize.Permission(orgPermission)
	}

	return serialize.Paginated(responses, totalCount), nil
}
