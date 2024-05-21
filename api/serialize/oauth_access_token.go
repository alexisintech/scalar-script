package serialize

import (
	"encoding/json"

	"clerk/model"
)

type OAuthAccessTokenResponse struct {
	Object            string          `json:"object"`
	ExternalAccountID string          `json:"external_account_id"`
	ProviderUserID    string          `json:"provider_user_id"`
	Token             string          `json:"token"`
	Provider          string          `json:"provider"`
	PublicMetadata    json.RawMessage `json:"public_metadata" logger:"omit"`
	Label             *string         `json:"label"`

	// Only set in OAuth 2.0 tokens
	Scopes []string `json:"scopes,omitempty"`

	// Only set in OAuth 1.0 tokens
	TokenSecret string `json:"token_secret,omitempty"`
}

func OAuth1AccessToken(extAccount *model.ExternalAccount) *OAuthAccessTokenResponse {
	r := oauthAccessToken(extAccount)
	r.Token = extAccount.AccessToken
	r.TokenSecret = extAccount.Oauth1AccessTokenSecret.String

	return r
}

func OAuth2AccessToken(extAccount *model.ExternalAccount, accessToken string, scopes []string) *OAuthAccessTokenResponse {
	r := oauthAccessToken(extAccount)
	r.Token = accessToken
	r.Scopes = scopes

	return r
}

func oauthAccessToken(extAccount *model.ExternalAccount) *OAuthAccessTokenResponse {
	r := &OAuthAccessTokenResponse{
		Object:            "oauth_access_token",
		ExternalAccountID: extAccount.ID,
		ProviderUserID:    extAccount.ProviderUserID,
		Provider:          extAccount.Provider,
		PublicMetadata:    json.RawMessage(extAccount.PublicMetadata),
	}

	if extAccount.Label.Valid {
		r.Label = &extAccount.Label.String
	}

	return r
}
