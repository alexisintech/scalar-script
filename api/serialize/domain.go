package serialize

import (
	"clerk/model"
)

const ObjectDomain = "domain"

type DomainResponse struct {
	Object            string        `json:"object"`
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	IsSatellite       bool          `json:"is_satellite"`
	FapiURL           string        `json:"frontend_api_url"`
	AccountsPortalURL *string       `json:"accounts_portal_url,omitempty"`
	ProxyURL          *string       `json:"proxy_url,omitempty"`
	CNameTargets      []CNameTarget `json:"cname_targets,omitempty"`
	DevelopmentOrigin string        `json:"development_origin"`
}

type CNameTarget struct {
	Host     string `json:"host"`
	Value    string `json:"value"`
	Required bool   `json:"required"`
}

type DomainOption func(*DomainResponse)

func WithCNameTargets(cnameTargets []CNameTarget) DomainOption {
	return func(response *DomainResponse) {
		response.CNameTargets = cnameTargets
	}
}

func WithDashboardDomainName(domain *model.Domain, instance *model.Instance) DomainOption {
	return func(response *DomainResponse) {
		response.Name = domain.NameForDashboard(instance)
	}
}

func Domain(domain *model.Domain, instance *model.Instance, options ...DomainOption) *DomainResponse {
	fapiURL := domain.FapiURL()

	var accountsURL *string
	if !domain.IsSatellite(instance) {
		url := domain.AccountsURL()
		accountsURL = &url
	}

	domainName := domain.Name
	if instance.IsDevelopment() && domain.DevName.Valid {
		domainName = domain.DevName.String
	}

	response := &DomainResponse{
		Object:            ObjectDomain,
		ID:                domain.ID,
		Name:              domainName,
		IsSatellite:       domain.IsSatellite(instance),
		FapiURL:           fapiURL,
		AccountsPortalURL: accountsURL,
		ProxyURL:          domain.ProxyURL.Ptr(),
		DevelopmentOrigin: domain.DevelopmentOrigin.String,
	}

	for _, option := range options {
		option(response)
	}
	return response
}
