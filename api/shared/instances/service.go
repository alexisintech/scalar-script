package instances

import (
	"context"
	"encoding/json"

	"clerk/api/shared/domains"
	"clerk/api/shared/edgereplication"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/generate"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	db                     database.Database
	domainRepo             *repository.Domain
	dnsChecksRepo          *repository.DNSChecks
	proxyChecksRepo        *repository.ProxyCheck
	instanceRepo           *repository.Instances
	edgeReplicationService *edgereplication.Service
}

func NewService(db database.Database, gueClient *gue.Client) *Service {
	return &Service{
		db:                     db,
		domainRepo:             repository.NewDomain(),
		dnsChecksRepo:          repository.NewDNSChecks(),
		proxyChecksRepo:        repository.NewProxyCheck(),
		instanceRepo:           repository.NewInstances(),
		edgeReplicationService: edgereplication.NewService(gueClient, cenv.GetBool(cenv.FlagReplicateInstanceToEdgeJobsEnabled)),
	}
}

// IsDeployed returns whether the instance is deployed or not.
// An instance is considered deployed when its primary domain has
// completed all the necessary deployment checks.
func (s *Service) IsDeployed(ctx context.Context, instance *model.Instance) (bool, error) {
	domain, err := s.domainRepo.FindByID(ctx, s.db, instance.ActiveDomainID)
	if err != nil {
		return false, err
	}

	var dnsCheck *model.DNSCheck
	if instance.IsProduction() {
		dnsCheck, err = s.dnsChecksRepo.QueryByDomainID(ctx, s.db, domain.ID)
		if err != nil {
			return false, err
		}
	}
	dnsOK, err := isDNSComplete(dnsCheck, instance)
	if err != nil {
		return false, err
	}
	proxyOK, err := s.isProxyVerified(ctx, domain)
	if err != nil {
		return false, err
	}
	return dnsOK &&
			proxyOK &&
			domain.SSLCheckComplete(instance) &&
			domain.MailCheckComplete(instance),
		nil
}

func isDNSComplete(
	dnsCheck *model.DNSCheck,
	instance *model.Instance,
) (bool, error) {
	if dnsCheck == nil {
		return instance.IsDevelopment(), nil
	}

	if dnsCheck.JobInflight {
		return false, nil
	}

	var cnameReqs generate.CNAMERequirements
	if err := json.Unmarshal(dnsCheck.CnameRequirements, &cnameReqs); err != nil {
		return false, err
	}
	var result generate.CNAMEResult
	if err := json.Unmarshal(dnsCheck.LastResult, &result); err != nil {
		return false, err
	}

	for host := range cnameReqs {
		isRequired := !cnameReqs[host].Optional
		isVerified := result[host]

		if isRequired && !isVerified {
			return false, nil
		}
	}

	return true, nil
}

func (s *Service) isProxyVerified(ctx context.Context, domain *model.Domain) (bool, error) {
	proxyCheck, err := s.proxyChecksRepo.QueryByDomainIDProxyURL(ctx, s.db, domain.ID, domain.ProxyURL.String)
	if err != nil {
		return false, err
	}
	proxyStatus := domains.GetProxyStatus(domain, proxyCheck)
	return !proxyStatus.Required || proxyStatus.Status == constants.ProxyComplete, nil
}

// UpdateSessionTokenTemplateID updates the session token template ID for the instance.
func (s *Service) UpdateSessionTokenTemplateID(ctx context.Context, tx database.Tx, instance *model.Instance, templateID *string) error {
	instance.SessionTokenTemplateID = null.StringFromPtr(templateID)
	err := s.instanceRepo.UpdateSessionTokenTemplateID(ctx, tx, instance)
	if err != nil {
		return err
	}
	return s.edgeReplicationService.EnqueuePutInstance(ctx, tx, instance.ID)
}
