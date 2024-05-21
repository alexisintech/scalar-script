package serialize

import (
	"context"

	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/time"
	"clerk/pkg/usersettings/clerk"
)

type InstanceResponse struct {
	Object              string                    `json:"object"`
	ID                  string                    `json:"id"`
	ApplicationID       string                    `json:"application_id"`
	EnvironmentType     constants.EnvironmentType `json:"environment_type"`
	HomeOrigin          *string                   `json:"home_origin"`
	CreatedAt           int64                     `json:"created_at"`
	UpdatedAt           int64                     `json:"updated_at"`
	ActiveDomain        *DomainResponse           `json:"active_domain" logger:"omit"`
	ActiveAuthConfig    *AuthConfigResponse       `json:"active_auth_config" logger:"omit"`
	ActiveDisplayConfig *DisplayConfigResponse    `json:"active_display_config" logger:"omit"`
}

type DemoDevInstanceResponse struct {
	Object             string `json:"object"`
	FAPIKey            string `json:"frontend_api_key"`
	BAPIKey            string `json:"backend_api_key"`
	JWTVerificationKey string `json:"jwt_verification_key"`
	AccountsURL        string `json:"accounts_url"`
}

type MinimalInstanceDashboardResponse struct {
	Object          string                    `json:"object"`
	ID              string                    `json:"id"`
	EnvironmentType constants.EnvironmentType `json:"environment_type"`
	HomeOrigin      *string                   `json:"home_origin,omitempty"`
}

func MinimalInstanceDashboard(ins *model.Instance) *MinimalInstanceDashboardResponse {
	return &MinimalInstanceDashboardResponse{
		Object:          "instance",
		ID:              ins.ID,
		EnvironmentType: constants.ToEnvironmentType(ins.EnvironmentType),
		HomeOrigin:      ins.HomeOrigin.Ptr(),
	}
}

func DemoDevInstance(instance *model.Instance, domain *model.Domain, keys *model.InstanceKey) *DemoDevInstanceResponse {
	if !instance.IsDevelopmentOrStaging() {
		panic("not a dev/stg instance")
	}

	return &DemoDevInstanceResponse{
		Object:             "demo_dev_instance",
		FAPIKey:            instance.PublishableKey(domain),
		BAPIKey:            keys.Secret,
		JWTVerificationKey: instance.PublicKeyForEdge(),
		AccountsURL:        domain.AccountsURL(),
	}
}

func Instance(ctx context.Context, env *model.Env, appImages *model.AppImages) *InstanceResponse {
	response := InstanceResponse{
		Object:          "instance",
		ID:              env.Instance.ID,
		ApplicationID:   env.Instance.ApplicationID,
		EnvironmentType: constants.ToEnvironmentType(env.Instance.EnvironmentType),
		HomeOrigin:      env.Instance.HomeOrigin.Ptr(),
		CreatedAt:       time.UnixMilli(env.Instance.CreatedAt),
		UpdatedAt:       time.UnixMilli(env.Instance.UpdatedAt),
	}

	userSettings := clerk.NewUserSettings(env.AuthConfig.UserSettings)
	authConfigResponse := AuthConfig(env.AuthConfig, userSettings, env.Instance.Communication)

	response.ActiveAuthConfig = authConfigResponse
	response.ActiveDomain = Domain(env.Domain, env.Instance)
	response.ActiveDisplayConfig = DisplayConfig(ctx, DisplayConfigParams{
		Env:       env,
		AppImages: appImages,
	})

	return &response
}
