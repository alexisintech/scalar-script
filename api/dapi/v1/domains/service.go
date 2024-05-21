package domains

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/domains"
	"clerk/api/shared/serializable"
	sharedserialize "clerk/api/shared/serialize"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/generate"
	"clerk/pkg/jobs"
	sdkutils "clerk/pkg/sdk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/domain"
	"github.com/clerk/clerk-sdk-go/v2/proxycheck" //nolint:staticcheck
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	clock                clockwork.Clock
	db                   database.Database
	gueClient            *gue.Client
	sdkConfigConstructor sdkutils.ConfigConstructor

	// services
	domainsService            *domains.Service
	serializableDomainService *serializable.DomainService

	// repositories
	domainRepo     *repository.Domain
	instanceRepo   *repository.Instances
	dnsChecksRepo  *repository.DNSChecks
	proxyCheckRepo *repository.ProxyCheck
}

func NewService(
	deps clerk.Deps,
	sdkConfigConstructor sdkutils.ConfigConstructor,
) *Service {
	return &Service{
		clock:                     deps.Clock(),
		db:                        deps.DB(),
		gueClient:                 deps.GueClient(),
		sdkConfigConstructor:      sdkConfigConstructor,
		domainsService:            domains.NewService(deps),
		serializableDomainService: serializable.NewDomainService(),
		domainRepo:                repository.NewDomain(),
		instanceRepo:              repository.NewInstances(),
		dnsChecksRepo:             repository.NewDNSChecks(),
		proxyCheckRepo:            repository.NewProxyCheck(),
	}
}

func (s *Service) Exists(ctx context.Context, name string) apierror.Error {
	exists, err := s.domainRepo.Exists(ctx, s.db, name)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if exists {
		return apierror.FormAlreadyExists("domain")
	}

	return nil
}

// List returns all the domains for the instance with instanceID, along
// with their status checks. Status checks include DNS, Email and SSL.
// Production instances use DNS check records to cache the result of the
// DNS checks. So, for production instances, we'll fetch all the domains,
// fetch their DNS checks and join them on the application level, so that
// we avoid N+1 queries.
func (s *Service) List(ctx context.Context, instanceID string) (*serialize.PaginatedResponse, apierror.Error) {
	instance, err := s.instanceRepo.QueryByID(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if instance == nil {
		return nil, apierror.ResourceNotFound()
	}

	domains, err := s.domainRepo.FindAllByInstanceIDWithModifiers(
		ctx,
		s.db,
		instanceID,
		repository.DomainFindAllByInstanceModifiers{
			OrderBy: s.domainRepo.WithOrderByCreatedAtAsc(),
		},
		nil,
	)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializableDomains, err := s.serializableDomainService.ConvertToSerializables(ctx, s.db, instance, domains)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	paginated := make([]any, len(domains))
	for i, serializableDomain := range serializableDomains {
		deployStatus, err := s.domainsService.GetDeployStatus(
			ctx,
			serializableDomain.Domain,
			serializableDomain.Instance,
			serializableDomain.DNSCheck,
			serializableDomain.ProxyCheck,
		)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		paginated[i] = sharedserialize.DomainWithChecks(serializableDomain.Domain, serializableDomain.Instance, deployStatus)
	}

	return serialize.Paginated(paginated, int64(len(paginated))), nil
}

func (s *Service) Create(
	ctx context.Context,
	instanceID string,
	params domain.CreateParams,
) (*sdk.Domain, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, sdkErr := sdkClient.Create(ctx, &params)
	return response, sdkutils.ToAPIError(sdkErr)
}

func (s *Service) Update(
	ctx context.Context,
	instanceID string,
	domainID string,
	params domain.UpdateParams,
) (*sdk.Domain, apierror.Error) {
	sdkConfig, apiErr := sdkutils.NewConfigForInstance(
		ctx,
		s.sdkConfigConstructor,
		s.db,
		instanceID,
	)
	if apiErr != nil {
		return nil, apiErr
	}

	if params.ProxyURL != nil && *params.ProxyURL != "" {
		//nolint:staticcheck
		_, sdkErr := proxycheck.NewClient(sdkConfig).Create(ctx, &proxycheck.CreateParams{
			DomainID: sdk.String(domainID),
			ProxyURL: params.ProxyURL,
		})
		if sdkErr != nil {
			return nil, sdkutils.ToAPIError(sdkErr)
		}
	}

	dmn, sdkErr := domain.NewClient(sdkConfig).Update(ctx, domainID, &params)
	return dmn, sdkutils.ToAPIError(sdkErr)
}

func (s *Service) UpdatePrimaryDomain(
	ctx context.Context,
	instanceID string,
	params domain.UpdateParams,
) (*sdk.Domain, apierror.Error) {
	env := environment.FromContext(ctx)
	return s.Update(ctx, instanceID, env.Instance.ActiveDomainID, params)
}

func (s *Service) Delete(
	ctx context.Context,
	instanceID string,
	domainID string,
) (*sdk.DeletedResource, apierror.Error) {
	sdkClient, apiErr := s.newSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	response, sdkErr := sdkClient.Delete(ctx, domainID)
	return response, sdkutils.ToAPIError(sdkErr)
}

func (s *Service) Status(
	ctx context.Context,
	instanceID,
	domainID string,
) (*sharedserialize.DomainStatusResponse, apierror.Error) {
	instance, domain, apiErr := s.queryInstanceAndDomainByID(ctx, instanceID, domainID)
	if apiErr != nil {
		return nil, apiErr
	}

	var err error
	var dnsCheck *model.DNSCheck
	if instance.IsProduction() {
		dnsCheck, err = s.dnsChecksRepo.QueryByDomainID(ctx, s.db, domain.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	var proxyCheck *model.ProxyCheck
	if domain.ProxyURL.Valid {
		proxyCheck, err = s.proxyCheckRepo.QueryByDomainIDProxyURL(ctx, s.db, domain.ID, domain.ProxyURL.String)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	res, err := s.domainsService.GetDeployStatus(ctx, domain, instance, dnsCheck, proxyCheck)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return res, nil
}

func (s *Service) queryInstanceAndDomainByID(
	ctx context.Context,
	instanceID, domainID string,
) (
	*model.Instance,
	*model.Domain,
	apierror.Error,
) {
	instance, err := s.instanceRepo.QueryByID(ctx, s.db, instanceID)
	if err != nil {
		return nil, nil, apierror.Unexpected(err)
	}
	if instance == nil {
		return nil, nil, apierror.InstanceNotFound(instanceID)
	}

	domain, err := s.domainRepo.QueryByID(ctx, s.db, domainID)
	if err != nil {
		return nil, nil, apierror.Unexpected(err)
	}
	if domain == nil {
		return nil, nil, apierror.DomainNotFound(domainID)
	}

	return instance, domain, nil
}

func (s *Service) RetrySSL(
	ctx context.Context,
	instanceID,
	domainID string,
) apierror.Error {
	instance, domain, err := s.queryInstanceAndDomainByID(ctx, instanceID, domainID)
	if err != nil {
		return err
	}

	if !instance.IsProduction() {
		return apierror.InstanceTypeInvalid()
	}
	if !domain.DeploymentStarted() {
		return apierror.InstanceNotLive()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.domainsService.ProvisionCertificate(ctx, tx, domain)
		return err != nil, err
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}
	return nil
}

// RetryDNS enqueues another job to perform a DNS check for a particular domain
func (s *Service) RetryDNS(
	ctx context.Context,
	instanceID, domainID string,
) apierror.Error {
	instance, domain, apiErr := s.queryInstanceAndDomainByID(ctx, instanceID, domainID)
	if apiErr != nil {
		return apiErr
	}

	if instance == nil {
		return apierror.InstanceNotFound(instanceID)
	}
	if !instance.IsProduction() {
		return apierror.InstanceTypeInvalid()
	}

	check, err := s.dnsChecksRepo.QueryByDomainID(ctx, s.db, domainID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if check == nil {
		check, err = generate.DNSCheck(ctx, s.db, instance, domain)
		if err != nil {
			return apierror.Unexpected(err)
		}
	}

	if check.JobInflight {
		return apierror.Conflict()
	}

	nextRunAt := check.LastRunAt.Time.Add(cenv.GetDurationInSeconds(cenv.DNSEntryCacheExpiryInSeconds))
	if nextRunAt.After(s.clock.Now()) { // Retried too early
		return apierror.Conflict()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err = jobs.CheckDNSRecords(ctx, s.gueClient,
			jobs.CheckDNSRecordsArgs{
				DomainID: check.DomainID,
			}, jobs.WithTx(tx))
		if err != nil {
			return true, err
		}

		check.JobInflight = true
		err = s.dnsChecksRepo.UpdateJobInflight(ctx, tx, check)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	return nil
}

// RetryMail retries the mail verification for a particular domain
func (s *Service) RetryMail(
	ctx context.Context,
	instanceID, domainID string,
) apierror.Error {
	instance, domain, apiErr := s.queryInstanceAndDomainByID(ctx, instanceID, domainID)
	if apiErr != nil {
		return apiErr
	}

	if !instance.IsProduction() {
		return apierror.InstanceTypeInvalid()
	}
	if domain.IsSatellite(instance) {
		return apierror.OperationNotAllowedOnSatelliteDomain()
	}
	if !domain.DeploymentStarted() {
		return apierror.InstanceNotLive()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.domainsService.ScheduleSendgridVerification(ctx, tx, domain)
		return err != nil, err
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}
	return nil
}

type VerifyProxyParams struct {
	ProxyURL   string `json:"proxy_url"`
	instanceID string `json:"-"`
	domainID   string `json:"-"`
}

func (s *Service) VerifyProxy(ctx context.Context, params VerifyProxyParams) (*sdk.ProxyCheck, apierror.Error) {
	sdkConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.sdkConfigConstructor, s.db, params.instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	//nolint:staticcheck
	proxyCheck, sdkErr := proxycheck.NewClient(sdkConfig).Create(ctx, &proxycheck.CreateParams{
		DomainID: sdk.String(params.domainID),
		ProxyURL: sdk.String(params.ProxyURL),
	})
	return proxyCheck, sdkutils.ToAPIError(sdkErr)
}

func (s *Service) newSDKClientForInstance(ctx context.Context, instanceID string) (*domain.Client, apierror.Error) {
	sdkConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.sdkConfigConstructor, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return domain.NewClient(sdkConfig), nil
}
