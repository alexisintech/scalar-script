package serialize

import (
	"clerk/model"

	"github.com/volatiletech/null/v8"
)

const AccountPortalObjectName = "account_portal"

type AccountPortalResponse struct {
	Object                      string      `json:"object"`
	InstanceID                  string      `json:"instance_id"`
	Enabled                     bool        `json:"enabled"`
	InternalLinking             bool        `json:"internal_linking"`
	AfterSignInPath             null.String `json:"after_sign_in_path"`
	AfterSignUpPath             null.String `json:"after_sign_up_path"`
	AfterCreateOrganizationPath null.String `json:"after_create_organization_path"`
	AfterLeaveOrganizationPath  null.String `json:"after_leave_organization_path"`
	LogoLinkPath                null.String `json:"logo_link_path"`
}

func AccountPortal(accountPortal *model.AccountPortal) *AccountPortalResponse {
	return &AccountPortalResponse{
		Object:                      AccountPortalObjectName,
		InstanceID:                  accountPortal.InstanceID,
		Enabled:                     accountPortal.Enabled,
		InternalLinking:             accountPortal.InternalLinking,
		AfterSignInPath:             accountPortal.Paths.AfterSignIn,
		AfterSignUpPath:             accountPortal.Paths.AfterSignUp,
		AfterCreateOrganizationPath: accountPortal.Paths.AfterCreateOrganization,
		AfterLeaveOrganizationPath:  accountPortal.Paths.AfterLeaveOrganization,
		LogoLinkPath:                accountPortal.Paths.LogoLink,
	}
}
