package serialize

import (
	"context"

	"clerk/api/serialize"
	"clerk/model"
	"clerk/pkg/constants"
	clerktime "clerk/pkg/time"
	"clerk/pkg/usersettings/clerk"
)

type InstanceResponse struct {
	Object                 string                                         `json:"object"`
	ID                     string                                         `json:"id"`
	ApplicationID          string                                         `json:"application_id"`
	EnvironmentType        constants.EnvironmentType                      `json:"environment_type"`
	HomeOrigin             *string                                        `json:"home_origin"`
	CreatedAt              int64                                          `json:"created_at"`
	UpdatedAt              int64                                          `json:"updated_at"`
	ActiveDomain           *serialize.DomainResponse                      `json:"active_domain" logger:"omit"`
	ActiveAuthConfig       *serialize.AuthConfigResponse                  `json:"active_auth_config" logger:"omit"`
	ActiveDisplayConfig    *serialize.DisplayConfigDashboardResponse      `json:"active_display_config" logger:"omit"`
	PremiumFeatures        []string                                       `json:"premium_features"`
	SupportedFeatures      []string                                       `json:"supported_features"`
	Features               map[string]*serialize.InstanceFeaturesResponse `json:"features"`
	AuthEmailsFrom         *string                                        `json:"auth_emails_from"`
	SessionTokenTemplateID *string                                        `json:"session_token_template_id"`
	FromEmailDomainName    string                                         `json:"from_email_domain_name"`
	PatchMePasswordState   string                                         `json:"patch_me_password_state"`
	APIVersion             string                                         `json:"api_version"`
	Billing                *BillingConfigResponse                         `json:"billing"`
	HasUsers               bool                                           `json:"has_users"`
	BlockedCountryCodes    []string                                       `json:"blocked_country_codes"`
	DevMonthlySMSLimit     *int                                           `json:"dev_monthly_sms_limit"`
}

type InstancesResponse []*InstanceResponse

func Instance(
	ctx context.Context,
	env *model.Env,
	appImages *model.AppImages,
	premiumFeatures []string,
	supportedFeatures []string,
	enabledPlans []*model.SubscriptionPlan,
	availablePlans []*model.SubscriptionPlan,
) *InstanceResponse {
	response := &InstanceResponse{
		Object:                 "instance",
		ID:                     env.Instance.ID,
		ApplicationID:          env.Instance.ApplicationID,
		EnvironmentType:        constants.ToEnvironmentType(env.Instance.EnvironmentType),
		HomeOrigin:             env.Instance.HomeOrigin.Ptr(),
		PremiumFeatures:        premiumFeatures,
		SupportedFeatures:      supportedFeatures,
		Features:               serialize.InstanceFeatures(env, enabledPlans, availablePlans),
		AuthEmailsFrom:         env.Instance.Communication.AuthEmailsFrom.Ptr(),
		SessionTokenTemplateID: env.Instance.SessionTokenTemplateID.Ptr(),
		FromEmailDomainName:    env.Domain.FromEmailDomainName(),
		CreatedAt:              clerktime.UnixMilli(env.Instance.CreatedAt),
		UpdatedAt:              clerktime.UnixMilli(env.Instance.UpdatedAt),
		APIVersion:             env.Instance.APIVersion,
		BlockedCountryCodes:    env.Instance.Communication.BlockedCountryCodes,
		DevMonthlySMSLimit:     getDevMonthlySMSLimit(env.Instance),
	}

	if env.Instance.ExternalBillingAccountID.Valid {
		response.Billing = &BillingConfigResponse{
			AccountID:     env.Instance.ExternalBillingAccountID.String,
			CustomerTypes: env.Instance.BillingCustomerTypes,
			PortalEnabled: env.Instance.BillingPortalEnabled.Bool,
		}
	}

	userSettings := clerk.NewUserSettings(env.AuthConfig.UserSettings)
	authConfigResponse := serialize.AuthConfig(env.AuthConfig, userSettings, env.Instance.Communication)

	response.ActiveAuthConfig = authConfigResponse
	response.ActiveDomain = serialize.Domain(
		env.Domain,
		env.Instance,
		serialize.WithDashboardDomainName(env.Domain, env.Instance),
	)
	response.ActiveDisplayConfig = serialize.DisplayConfigForDashboardAPI(ctx,
		serialize.DisplayConfigDAPIParams{
			Env:       env,
			AppImages: appImages,
		})
	response.PatchMePasswordState = env.AuthConfig.ExperimentalSettings.PatchMePasswordStateValue()
	return response
}

func getDevMonthlySMSLimit(instance *model.Instance) *int {
	if instance.IsProduction() {
		return nil
	}

	limit := instance.Communication.GetDevMonthlySMSLimit()

	return &limit
}
