package serialize

import (
	"context"

	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/usersettings/clerk"
	usersettings "clerk/pkg/usersettings/model"
)

type EnvironmentResponse struct {
	AuthConfig           *authConfigEnvironmentResponse `json:"auth_config" logger:"omit"`
	DisplayConfig        *DisplayConfigResponse         `json:"display_config"`
	UserSettings         environmentUserSettings        `json:"user_settings"`
	OrganizationSettings *organizationSettingsResponse  `json:"organization_settings"`
	MaintenanceMode      bool                           `json:"maintenance_mode"`
}

// We need to include the allowed special characters in password settings, so
// that the FE can do the necessary checks.
type environmentUserSettings struct {
	usersettings.UserSettings
	Social           map[string]environmentSocialSettings `json:"social"`
	PasswordSettings environmentPasswordSettings          `json:"password_settings"`
	Billing          *environmentBillingSettings          `json:"billing,omitempty"`
}

type environmentSocialSettings struct {
	Enabled                bool   `json:"enabled"`
	Required               bool   `json:"required"`
	Authenticatable        bool   `json:"authenticatable"`
	BlockEmailSubaddresses bool   `json:"block_email_subaddresses"`
	Strategy               string `json:"strategy"`
	NotSelectable          bool   `json:"not_selectable"`
	Deprecated             bool   `json:"deprecated"`
}

type environmentPasswordSettings struct {
	usersettings.PasswordSettings
	AllowedSpecialCharacters string `json:"allowed_special_characters"`
}

type environmentBillingSettings struct {
	Enabled       bool `json:"enabled"`
	PortalEnabled bool `json:"portal_enabled"`
}

// NOTE: FAPI /v1/environment is cached at edge. Therefore, changes to this
// endpoint also require us to purge the caches. See docs/edge_caching.md.
func Environment(ctx context.Context, env *model.Env, appImages *model.AppImages, devBrowser *model.DevBrowser, googleOneTapClientID *string) *EnvironmentResponse {
	authConfigResponse := AuthConfig(env.AuthConfig, clerk.NewUserSettings(env.AuthConfig.UserSettings), env.Instance.Communication)

	return &EnvironmentResponse{
		AuthConfig: &authConfigEnvironmentResponse{
			AuthConfigResponse: authConfigResponse,
			Demo:               env.Application.Demo,
		},
		DisplayConfig: DisplayConfig(ctx, DisplayConfigParams{
			Env:                  env,
			AppImages:            appImages,
			DevBrowser:           devBrowser,
			GoogleOneTapClientID: googleOneTapClientID,
		}),
		UserSettings:         userSettings(env),
		OrganizationSettings: organizationSettings(env),
		MaintenanceMode:      cenv.IsEnabled(cenv.ClerkMaintenanceMode),
	}
}
