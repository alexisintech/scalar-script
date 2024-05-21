package serialize

type SystemFeatureFlags struct {
	AllowNewPricingCheckout  bool `json:"allow_new_pricing_checkout"`
	AllowOrganizationBilling bool `json:"allow_organization_billing"`
}

type Notifications struct {
	ShowSAMLGA bool `json:"show_saml_ga"`
}

type SystemConfigResponse struct {
	Object            string             `json:"object"`
	OAuthProviders    []string           `json:"oauth_providers"`
	Web3Providers     []string           `json:"web3_providers"`
	SupportedFeatures []string           `json:"supported_features"`
	FeatureFlags      SystemFeatureFlags `json:"feature_flags"`
	Notifications     Notifications      `json:"notifications"`
}

func SystemConfig(
	oauthProviders, web3Providers, supportedFeatures []string,
	featureFlags SystemFeatureFlags,
	notifications Notifications) *SystemConfigResponse {
	return &SystemConfigResponse{
		Object:            "system_config",
		OAuthProviders:    oauthProviders,
		Web3Providers:     web3Providers,
		SupportedFeatures: supportedFeatures,
		FeatureFlags:      featureFlags,
		Notifications:     notifications,
	}
}
