package serialize

import (
	"clerk/pkg/organizationsettings"
)

const ObjectOrganizationSettings = "organization_settings"

type OrganizationSettingsResponse struct {
	Object                 string   `json:"object"`
	Enabled                bool     `json:"enabled"`
	MaxAllowedMemberships  int      `json:"max_allowed_memberships"`
	MaxAllowedRoles        int      `json:"max_allowed_roles"`
	MaxAllowedPermissions  int      `json:"max_allowed_permissions"`
	CreatorRole            string   `json:"creator_role"`
	AdminDeleteEnabled     bool     `json:"admin_delete_enabled"`
	DomainsEnabled         bool     `json:"domains_enabled"`
	DomainsEnrollmentModes []string `json:"domains_enrollment_modes"`
	DomainsDefaultRole     string   `json:"domains_default_role"`
}

func OrganizationSettings(settings organizationsettings.OrganizationSettings) *OrganizationSettingsResponse {
	return &OrganizationSettingsResponse{
		Object:                 ObjectOrganizationSettings,
		Enabled:                settings.Enabled,
		MaxAllowedMemberships:  settings.MaxAllowedMemberships,
		MaxAllowedRoles:        settings.MaxAllowedRoles,
		MaxAllowedPermissions:  settings.MaxAllowedPermissions,
		CreatorRole:            settings.CreatorRole,
		AdminDeleteEnabled:     settings.Actions.AdminDelete,
		DomainsEnabled:         settings.Domains.Enabled,
		DomainsEnrollmentModes: settings.Domains.SortedEnrollmentModes(),
		DomainsDefaultRole:     settings.Domains.DefaultRole,
	}
}
