package serialize

import (
	"clerk/api/sapi/v1/serializable"
	"clerk/model"
)

type UserLimitsResponse struct {
	MaxUserLimit int `json:"max_user_limit"`
}

type OrganizationSettingsResponse struct {
	CreationLimit     int `json:"creation_limit"`
	MembershipLimit   int `json:"membership_limit"`
	RoleCreationLimit int `json:"role_creation_limit"`
	PermissionLimit   int `json:"permissions_limit"`
}

type SMSSettingsResponse struct {
	MaxPrice           float32 `json:"max_price"`
	DevMonthlySMSLimit *int    `json:"dev_monthly_sms_limit"`
}

type SubscriptionResponse struct {
	Plan   string   `json:"plan"`
	Addons []string `json:"addons"`
}

type ActiveDomainResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Deployed bool   `json:"deployed"`
}

type InstanceResponse struct {
	ID                   string                       `json:"id"`
	EnvironmentType      string                       `json:"environment_type"`
	ApplicationID        string                       `json:"application_id"`
	ApplicationName      string                       `json:"application_name"`
	UserLimits           UserLimitsResponse           `json:"user_limits"`
	OrganizationSettings OrganizationSettingsResponse `json:"organization_settings"`
	SMSSettings          SMSSettingsResponse          `json:"sms_settings"`
	Subscription         SubscriptionResponse         `json:"subscription"`
	ActiveDomain         ActiveDomainResponse         `json:"active_domain"`
}

func Instance(instanceSerializable *serializable.Instance) *InstanceResponse {
	addons := make([]string, len(instanceSerializable.Addons))
	for i, addon := range instanceSerializable.Addons {
		addons[i] = addon.Title
	}
	return &InstanceResponse{
		ID:              instanceSerializable.Env.Instance.ID,
		EnvironmentType: instanceSerializable.Env.Instance.EnvironmentType,
		ApplicationID:   instanceSerializable.Env.Application.ID,
		ApplicationName: instanceSerializable.Env.Application.Name,
		UserLimits: UserLimitsResponse{
			MaxUserLimit: instanceSerializable.Env.AuthConfig.MaxAllowedUsers.Int,
		},
		OrganizationSettings: OrganizationSettingsResponse{
			CreationLimit:     instanceSerializable.Env.AuthConfig.OrganizationSettings.CreateQuotaPerUser,
			MembershipLimit:   instanceSerializable.Env.AuthConfig.OrganizationSettings.MaxAllowedMemberships,
			RoleCreationLimit: instanceSerializable.Env.AuthConfig.OrganizationSettings.MaxAllowedRoles,
			PermissionLimit:   instanceSerializable.Env.AuthConfig.OrganizationSettings.MaxAllowedPermissions,
		},
		SMSSettings: SMSSettingsResponse{
			MaxPrice:           instanceSerializable.Env.Instance.Communication.SMSMaxPrice(),
			DevMonthlySMSLimit: getDevMonthlySMSLimit(instanceSerializable.Env.Instance),
		},
		Subscription: SubscriptionResponse{
			Plan:   instanceSerializable.BasicPlan.Title,
			Addons: addons,
		},
		ActiveDomain: ActiveDomainResponse{
			ID:       instanceSerializable.ActiveDomain.ID,
			Name:     instanceSerializable.ActiveDomain.NameForDashboard(instanceSerializable.Env.Instance),
			Deployed: instanceSerializable.IsActiveDomainDeployed,
		},
	}
}

func getDevMonthlySMSLimit(instance *model.Instance) *int {
	if instance.IsProduction() {
		return nil
	}

	limit := instance.Communication.GetDevMonthlySMSLimit()

	return &limit
}
