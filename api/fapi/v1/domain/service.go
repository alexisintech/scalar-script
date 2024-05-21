package domain

import (
	"context"
	"fmt"
	"strings"

	"clerk/api/apierror"
	"clerk/api/shared/kima_hosts"
	"clerk/model"
	"clerk/pkg/ctx/requestdomain"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	db              database.Database
	domainRepo      *repository.Domain
	instanceKeyRepo *repository.InstanceKeys
}

func NewService(db database.Database) *Service {
	return &Service{
		db:              db,
		domainRepo:      repository.NewDomain(),
		instanceKeyRepo: repository.NewInstanceKeys(),
	}
}

type SetDomainParams struct {
	Host               string
	ProxyURL           string
	SecretKey          string
	DevSatelliteDomain string
}

func (s *Service) SetDomainFromRequest(ctx context.Context, params SetDomainParams) (context.Context, apierror.Error) {
	var domain *model.Domain
	var apiErr apierror.Error

	if kima_hosts.UsesNewFapiDevDomain(params.Host) {
		domain, apiErr = s.fetchByDevSlug(ctx, params.Host, params.DevSatelliteDomain)
	} else if params.ProxyURL != "" {
		// request is using proxy
		domain, apiErr = s.fetchByProxy(ctx, params.ProxyURL, params.SecretKey)
	} else {
		domain, apiErr = s.fetchByName(ctx, params.Host)
	}
	if apiErr != nil {
		return ctx, apiErr
	}
	if domain == nil {
		return ctx, apierror.InvalidHost()
	}

	return requestdomain.NewContext(ctx, domain), nil
}

func (s *Service) fetchByDevSlug(ctx context.Context, host, devSatelliteDomain string) (*model.Domain, apierror.Error) {
	hostParts := strings.Split(host, ".")
	if len(hostParts) < 3 {
		return nil, apierror.InvalidHost()
	}

	domain, err := s.domainRepo.QueryByDevSlug(ctx, s.db, hostParts[0])
	if err != nil {
		return nil, apierror.Unexpected(fmt.Errorf("environment/load: by dev_slug (%s): %w",
			hostParts[0], err))
	}

	if devSatelliteDomain != "" && domain != nil {
		// caller is trying to access a satellite domain of that instance
		domain, err = s.domainRepo.QueryByDevNameAndInstance(ctx, s.db, devSatelliteDomain, domain.InstanceID)
		if err != nil {
			return nil, apierror.Unexpected(fmt.Errorf("environment/load: by dev_name=%s and instance=%s: %w",
				devSatelliteDomain, domain.InstanceID, err))
		}
	}
	return domain, nil
}

// fetchByName returns a model.Domain whose name matches either the first or
// second part of the provided host. Returns an error if no domain is found,
// or the domain is not associated with an instance.
func (s *Service) fetchByName(ctx context.Context, host string) (*model.Domain, apierror.Error) {
	hostParts := strings.Split(host, ".")
	if len(hostParts) < 3 {
		return nil, apierror.InvalidHost()
	}
	domainName1 := strings.Join(hostParts[1:], ".")
	domainName2 := strings.Join(hostParts[2:], ".")

	domain, err := s.domainRepo.QueryByName(ctx, s.db, domainName1, domainName2)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return domain, nil
}

// fetchByProxy returns a model.Domain by proxyURL.
// Returns an error if no domain is found, or the domain doesn't have
// an instance or the secret key provided is invalid.
func (s *Service) fetchByProxy(ctx context.Context, proxyURL, secretKey string) (*model.Domain, apierror.Error) {
	if secretKey == "" {
		return nil, apierror.ProxyRequestMissingSecretKey()
	}

	domain, err := s.domainRepo.QueryByProxyURL(ctx, s.db, proxyURL)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if domain == nil {
		return nil, apierror.InvalidHost()
	}

	secretKeyExists, err := s.instanceKeyRepo.ExistsBySecretAndInstance(ctx, s.db, secretKey, domain.InstanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if !secretKeyExists {
		return nil, apierror.ProxyRequestInvalidSecretKey()
	}

	return domain, nil
}
