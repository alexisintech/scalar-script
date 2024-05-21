package serialize

import (
	"clerk/model"
	"clerk/pkg/paths"
)

const AccountPortalObjectName = "account_portal"

type AccountPortalFAPIResponse struct {
	Object                     string `json:"object"`
	Allowed                    bool   `json:"allowed"`
	Enabled                    bool   `json:"enabled"`
	InternalLinking            bool   `json:"internal_linking"`
	AfterSignInURL             string `json:"after_sign_in_url"`
	AfterSignUpURL             string `json:"after_sign_up_url"`
	AfterCreateOrganizationURL string `json:"after_create_organization_url"`
	AfterLeaveOrganizationURL  string `json:"after_leave_organization_url"`
	LogoLinkURL                string `json:"logo_link_url"`
}

func AccountPortalFAPI(
	accountPortal *model.AccountPortal,
	app *model.Application,
	instance *model.Instance,
	domain *model.Domain,
	devBrowser *model.DevBrowser,
) *AccountPortalFAPIResponse {
	origin := instance.Origin(domain, devBrowser)
	accountsURL := domain.AccountsURL()
	fallbackURL := paths.DefaultHomeURL(origin, accountsURL)
	enabled := accountPortal.Enabled && domain.IsPrimary(instance)

	return &AccountPortalFAPIResponse{
		Object:                     AccountPortalObjectName,
		Allowed:                    app.AccountPortalAllowed,
		Enabled:                    enabled,
		InternalLinking:            accountPortal.InternalLinking,
		AfterSignInURL:             accountPortal.Paths.AfterSignInURL(origin, fallbackURL),
		AfterSignUpURL:             accountPortal.Paths.AfterSignUpURL(origin, fallbackURL),
		AfterCreateOrganizationURL: accountPortal.Paths.AfterCreateOrganizationURL(origin, fallbackURL),
		AfterLeaveOrganizationURL:  accountPortal.Paths.AfterLeaveOrganizationURL(origin, fallbackURL),
		LogoLinkURL:                accountPortal.Paths.LogoLinkURL(origin, fallbackURL),
	}
}
