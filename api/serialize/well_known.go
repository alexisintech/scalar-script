package serialize

import (
	"encoding/json"

	"clerk/api/apierror"
	"clerk/model"
)

type AppleAppSiteAssociationResponseDetail struct {
	AppID string   `json:"appID"`
	Paths []string `json:"paths"`
}

type AppleAppSiteAssociationResponseApplinks struct {
	Apps    []string                                `json:"apps"`
	Details []AppleAppSiteAssociationResponseDetail `json:"details"`
}

type AppleAppSiteAssociationResponseWebCredentials struct {
	Apps []string `json:"apps"`
}

type AppleAppSiteAssociationResponse struct {
	Applinks       AppleAppSiteAssociationResponseApplinks        `json:"applinks"`
	WebCredentials *AppleAppSiteAssociationResponseWebCredentials `json:"webcredentials,omitempty"`
}

// https://developer.apple.com/documentation/bundleresources/applinks
func AppleAppSiteAssociation(appID string, paths []string, includeWebCredentials bool) *AppleAppSiteAssociationResponse {
	details := &AppleAppSiteAssociationResponseDetail{
		AppID: appID,
		Paths: paths,
	}

	applinks := &AppleAppSiteAssociationResponseApplinks{
		Apps:    []string{appID},
		Details: []AppleAppSiteAssociationResponseDetail{*details},
	}

	response := &AppleAppSiteAssociationResponse{
		Applinks: *applinks,
	}

	if includeWebCredentials {
		webcredentials := &AppleAppSiteAssociationResponseWebCredentials{
			Apps: []string{appID},
		}
		response.WebCredentials = webcredentials
	}

	return response
}

type AndroidAssetLinksResponseTarget struct {
	Namespace              string   `json:"namespace"`
	PackageName            string   `json:"package_name"`
	Sha256CERTFingerprints []string `json:"sha256_cert_fingerprints"`
}

type AndroidAssetLinksResponse struct {
	Relation []string                        `json:"relation"`
	Target   AndroidAssetLinksResponseTarget `json:"target"`
}

// https://developer.android.com/training/app-links/verify-site-associations
func AssetLinks(target []byte) ([]*AndroidAssetLinksResponse, apierror.Error) {
	t := AndroidAssetLinksResponseTarget{}

	err := json.Unmarshal(target, &t)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := []*AndroidAssetLinksResponse{{
		Relation: []string{"delegate_permission/common.handle_all_urls"},
		Target:   t,
	}}

	return response, nil
}

type OpenIDConfigurationResponse struct {
	Issuer                            string   `json:"issuer"`
	JwksURI                           string   `json:"jwks_uri"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	BackchannelLogoutSupported        bool     `json:"backchannel_logout_supported"`
	FrontchannelLogoutSupported       bool     `json:"frontchannel_logout_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported"`
	ResponseModesSupported            []string `json:"response_modes_supported"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported"`
	UserInfoEndpoint                  string   `json:"userinfo_endpoint"`
}

// https://www.jeremydaly.com/verifying-self-signed-jwt-tokens-with-aws-http-apis/
func OpenIDConfiguration(domain *model.Domain) *OpenIDConfigurationResponse {
	return &OpenIDConfigurationResponse{
		Issuer:                            domain.FapiURL(),
		JwksURI:                           domain.JwksURL(),
		AuthorizationEndpoint:             domain.OAuthAuthorizeURL(),
		BackchannelLogoutSupported:        false,
		FrontchannelLogoutSupported:       false,
		GrantTypesSupported:               []string{"authorization_code"},
		ResponseModesSupported:            []string{"form_post"},
		ResponseTypesSupported:            []string{"code"},
		TokenEndpoint:                     domain.OAuthTokenURL(),
		TokenEndpointAuthMethodsSupported: []string{"client_secret_post"},
		UserInfoEndpoint:                  domain.OAuthUserInfoURL(),
	}
}
