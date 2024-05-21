package serializable

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/repository"
	"clerk/utils/database"
)

type DomainService struct {
	domainRepo     *repository.Domain
	dnsChecksRepo  *repository.DNSChecks
	proxyCheckRepo *repository.ProxyCheck
}

func NewDomainService() *DomainService {
	return &DomainService{
		domainRepo:     repository.NewDomain(),
		dnsChecksRepo:  repository.NewDNSChecks(),
		proxyCheckRepo: repository.NewProxyCheck(),
	}
}

type Domain struct {
	Domain   *model.Domain
	Instance *model.Instance

	DNSCheck   *model.DNSCheck
	ProxyCheck *model.ProxyCheck
}

func (s *DomainService) ConvertToSerializables(ctx context.Context, exec database.Executor, instance *model.Instance, domains []*model.Domain) ([]*Domain, error) {
	domainIDs := make([]string, len(domains))
	for i, dmn := range domains {
		domainIDs[i] = dmn.ID
	}

	// For production instances, we'll need the DNS checks
	// associated with the domains.
	dnsChecksForDomains := map[string]*model.DNSCheck{}
	if instance.IsProduction() {
		dnsChecks, err := s.dnsChecksRepo.FindAllByDomainIDs(ctx, exec, domainIDs)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		for _, dc := range dnsChecks {
			dnsChecksForDomains[dc.DomainID] = dc
		}
	}

	// We'll need the proxy checks associated with the domains.
	// We'll add all the proxy checks we can find for the domains, even if
	// they reference another proxy URL than the current one on the domain.
	// Going through our proxy checks store guarantees that we'll get back
	// the correct proxy check later, when we retrieve it from the store for
	// each domain.
	proxyChecks, err := s.proxyCheckRepo.FindAllByDomainIDs(ctx, exec, domainIDs)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	proxyChecksForDomains := newProxyChecksStore()
	proxyChecksForDomains.Add(proxyChecks...)

	var serializableDomains []*Domain
	for _, domain := range domains {
		serializableDomains = append(serializableDomains, &Domain{
			Domain:     domain,
			Instance:   instance,
			DNSCheck:   dnsChecksForDomains[domain.ID],
			ProxyCheck: proxyChecksForDomains.Get(domain.ID, domain.ProxyURL.String),
		})
	}

	return serializableDomains, nil
}

// Groups proxy checks by domain ID.
// This type exists only to aid in joining domains with their current
// proxy checks. It's used in the List endpoint.
type proxyChecksStore struct {
	store map[string]*model.ProxyCheck
}

func newProxyChecksStore() *proxyChecksStore {
	return &proxyChecksStore{
		store: map[string]*model.ProxyCheck{},
	}
}

// Creates a key for the proxy checks store by concatenating a domain
// ID and a proxy URL.
func proxyChecksStoreKey(domainID, proxyURL string) string {
	return fmt.Sprintf("%s|%s", domainID, proxyURL)
}

// Add inserts the proxy checks to the store using the domain ID and proxy URL
// as a key.
// Warning: This method is not thread safe.
func (m *proxyChecksStore) Add(pcs ...*model.ProxyCheck) {
	for _, pc := range pcs {
		if pc.DomainID == "" || pc.ProxyURL == "" {
			continue
		}
		m.store[proxyChecksStoreKey(pc.DomainID, pc.ProxyURL)] = pc
	}
}

// Get retrieves a proxy check from the store by the domain ID and proxy URL
// key.
func (m *proxyChecksStore) Get(domainID, proxyURL string) *model.ProxyCheck {
	return m.store[proxyChecksStoreKey(domainID, proxyURL)]
}
