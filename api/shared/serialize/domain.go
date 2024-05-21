package serialize

import (
	"clerk/api/serialize"
	"clerk/model"
)

type DomainWithChecksResponse struct {
	serialize.DomainResponse
	Checks *DomainStatusResponse `json:"checks"`
}

func DomainWithChecks(
	domain *model.Domain,
	instance *model.Instance,
	checks *DomainStatusResponse,
) *DomainWithChecksResponse {
	return &DomainWithChecksResponse{
		DomainResponse: *serialize.Domain(
			domain,
			instance,
			serialize.WithDashboardDomainName(domain, instance),
		),
		Checks: checks,
	}
}
