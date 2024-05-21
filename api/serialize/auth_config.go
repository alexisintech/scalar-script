package serialize

import (
	"sort"

	"clerk/model"
	"clerk/pkg/communication"
	"clerk/pkg/constants"
	"clerk/pkg/organizationsettings"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/utils/validate"
)

type AuthConfigResponse struct {
	Object                             string     `json:"object"`
	ID                                 string     `json:"id"`
	FirstName                          string     `json:"first_name"`
	LastName                           string     `json:"last_name"`
	EmailAddress                       string     `json:"email_address"`
	PhoneNumber                        string     `json:"phone_number"`
	Username                           string     `json:"username"`
	Password                           string     `json:"password"`
	IdentificationRequirements         [][]string `json:"identification_requirements" logger:"omit"`
	IdentificationStrategies           []string   `json:"identification_strategies" logger:"omit"`
	FirstFactors                       []string   `json:"first_factors"`
	SecondFactors                      []string   `json:"second_factors"`
	EmailAddressVerificationStrategies []string   `json:"email_address_verification_strategies" logger:"omit"`
	SingleSessionMode                  bool       `json:"single_session_mode"`
	EnhancedEmailDeliverability        bool       `json:"enhanced_email_deliverability"`
	TestMode                           bool       `json:"test_mode"`

	// CookielessDev is true if this is a development instance and should
	// operate without cookies.
	//
	// Deprecated: Please use URLBasedSessionSyncing instead
	CookielessDev bool `json:"cookieless_dev"`

	// URLBasedSessionSyncing is true if this is a development instance and should
	// operate without cookies.
	URLBasedSessionSyncing bool `json:"url_based_session_syncing"`
}

type authConfigEnvironmentResponse struct {
	*AuthConfigResponse

	// Demo is true if the instance is an ephemeral development instance, i.e.
	// not claimed by any user.
	Demo bool `json:"demo"`
}

type organizationSettingsResponse struct {
	Enabled               bool                                  `json:"enabled"`
	MaxAllowedMemberships int                                   `json:"max_allowed_memberships"`
	Actions               organizationsettings.ActionsSettings  `json:"actions"`
	Domains               organizationsettings.DomainsSettings  `json:"domains"`
	CreatorRole           string                                `json:"creator_role"`
	Billing               *organizationsettings.BillingSettings `json:"billing,omitempty"`
}

func AuthConfig(ac *model.AuthConfig, userSettings *usersettings.UserSettings, comm communication.Communication) *AuthConfigResponse {
	identificationRequirements := userSettings.WeirdDeprecatedIdentificationRequirementsDoubleArray()
	sort.Strings(identificationRequirements[0])
	sort.Strings(identificationRequirements[1])

	emailAddressVerificationStrategies := userSettings.GetAttribute(names.EmailAddress).Base().Verifications
	sort.Strings(emailAddressVerificationStrategies)

	return &AuthConfigResponse{
		Object:                             "auth_config",
		ID:                                 ac.ID,
		FirstName:                          attributeToOldStatus(userSettings.GetAttribute(names.FirstName), true),
		LastName:                           attributeToOldStatus(userSettings.GetAttribute(names.LastName), true),
		EmailAddress:                       attributeToOldStatus(userSettings.GetAttribute(names.EmailAddress), false),
		PhoneNumber:                        attributeToOldStatus(userSettings.GetAttribute(names.PhoneNumber), false),
		Username:                           attributeToOldStatus(userSettings.GetAttribute(names.Username), false),
		Password:                           attributeToOldStatus(userSettings.GetAttribute(names.Password), true),
		IdentificationStrategies:           set.SortedStringSet(userSettings.IdentificationStrategies()),
		IdentificationRequirements:         identificationRequirements,
		FirstFactors:                       set.SortedStringSet(userSettings.FirstFactors()),
		SecondFactors:                      set.SortedStringSet(userSettings.SecondFactors()),
		EmailAddressVerificationStrategies: emailAddressVerificationStrategies,
		SingleSessionMode:                  ac.SessionSettings.SingleSessionMode,
		EnhancedEmailDeliverability:        comm.EnhancedEmailDeliverability,
		TestMode:                           ac.TestMode,
		CookielessDev:                      ac.SessionSettings.URLBasedSessionSyncing,
		URLBasedSessionSyncing:             ac.SessionSettings.URLBasedSessionSyncing,
	}
}

func attributeToOldStatus(attribute usersettings.Attribute, useRequired bool) string {
	status := constants.ACOff
	if attribute.Base().Required && useRequired {
		status = constants.ACRequired
	} else if attribute.Base().Enabled {
		status = constants.ACOn
	}
	return status
}

// Server API specific representation of auth_config
type AuthConfigResponseServer struct {
	Object                      string `json:"object"`
	RestrictedToAllowlist       bool   `json:"restricted_to_allowlist"`
	FromEmailAddress            string `json:"from_email_address"`
	ProgressiveSignUp           bool   `json:"progressive_sign_up"`
	TestMode                    bool   `json:"test_mode"`
	EnhancedEmailDeliverability bool   `json:"enhanced_email_deliverability"`
}

func AuthConfigToServerAPI(ac *model.AuthConfig, ins *model.Instance) *AuthConfigResponseServer {
	return &AuthConfigResponseServer{
		Object:                      "instance_settings",
		RestrictedToAllowlist:       ac.UserSettings.Restrictions.Allowlist.Enabled,
		FromEmailAddress:            ins.Communication.AuthEmailsFromAddressLocalPart(),
		ProgressiveSignUp:           ac.UserSettings.SignUp.Progressive,
		TestMode:                    ac.TestMode,
		EnhancedEmailDeliverability: ins.Communication.EnhancedEmailDeliverability,
	}
}

func userSettings(env *model.Env) environmentUserSettings {
	res := environmentUserSettings{
		UserSettings: env.AuthConfig.UserSettings,
		Social:       socialSettings(env.AuthConfig),
		PasswordSettings: environmentPasswordSettings{
			PasswordSettings:         env.AuthConfig.UserSettings.PasswordSettings,
			AllowedSpecialCharacters: validate.AllowedSpecialCharacters,
		},
	}
	if env.Instance.HasBillingEnabledForUsers() {
		res.Billing = &environmentBillingSettings{
			Enabled:       true,
			PortalEnabled: env.Instance.BillingPortalEnabled.Bool,
		}
	}
	return res
}

func organizationSettings(env *model.Env) *organizationSettingsResponse {
	settings := env.AuthConfig.OrganizationSettings
	res := &organizationSettingsResponse{
		Enabled:               settings.Enabled,
		MaxAllowedMemberships: settings.MaxAllowedMemberships,
		Actions:               settings.Actions,
		Domains:               settings.Domains,
		CreatorRole:           settings.CreatorRole,
	}
	if env.Instance.HasBillingEnabledForOrganizations() {
		res.Billing = &organizationsettings.BillingSettings{
			Enabled:       true,
			PortalEnabled: env.Instance.BillingPortalEnabled.Bool,
		}
	}
	return res
}

func socialSettings(authConfig *model.AuthConfig) map[string]environmentSocialSettings {
	resp := make(map[string]environmentSocialSettings)
	for provider, settings := range authConfig.UserSettings.Social {
		resp[provider] = environmentSocialSettings{
			Enabled:                settings.Enabled,
			Required:               settings.Required,
			Authenticatable:        settings.Authenticatable,
			BlockEmailSubaddresses: settings.BlockEmailSubaddresses,
			Strategy:               settings.Strategy,
			NotSelectable:          settings.NotSelectable,
			Deprecated:             settings.Deprecated,
		}
	}

	return resp
}
