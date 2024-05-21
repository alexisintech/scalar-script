package domains

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/domains"
	"clerk/api/shared/pagination"
	"clerk/api/shared/serializable"
	sharedserialize "clerk/api/shared/serialize"
	"clerk/pkg/ctx/environment"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	domainService             *domains.Service
	serializableDomainService *serializable.DomainService

	domainRepo   *repository.Domain
	instanceRepo *repository.Instances
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                        clerk.DB(),
		domainService:             domains.NewService(deps),
		serializableDomainService: serializable.NewDomainService(),
		domainRepo:                repository.NewDomain(),
		instanceRepo:              repository.NewInstances(),
	}
}

type ListParams struct {
	Pagination pagination.Params
}

func (s *Service) List(ctx context.Context, params ListParams) (any, apierror.Error) {
	env := environment.FromContext(ctx)

	domains, err := s.domainRepo.FindAllByInstanceIDWithModifiers(
		ctx,
		s.db,
		env.Instance.ID,
		repository.DomainFindAllByInstanceModifiers{
			OrderBy: s.domainRepo.WithOrderByCreatedAtAsc(),
		},
		&params.Pagination,
	)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.domainRepo.CountByInstanceID(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializableDomains, err := s.serializableDomainService.ConvertToSerializables(ctx, s.db, env.Instance, domains)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializedDomainResponses := make([]any, len(domains))
	for i, serializableDomain := range serializableDomains {
		deployStatus, err := s.domainService.GetDeployStatus(
			ctx,
			serializableDomain.Domain,
			serializableDomain.Instance,
			serializableDomain.DNSCheck,
			serializableDomain.ProxyCheck,
		)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		serializedDomainResponses[i] = sharedserialize.DomainWithChecks(serializableDomain.Domain, serializableDomain.Instance, deployStatus)
	}

	return serialize.Paginated(serializedDomainResponses, totalCount), nil
}
