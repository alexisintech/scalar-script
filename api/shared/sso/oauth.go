// Package sso provides single sign-on related functionality.
package sso

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/oauth"
	"clerk/pkg/oauth/provider"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/queries"
)

// RegisterOAuthProviders enables our currently supported OAuth providers.
func RegisterOAuthProviders() {
	oauth.RegisterProviders(
		provider.Google{},
		provider.Facebook{},
		provider.Github{},
		provider.Hubspot{},
		provider.Tiktok{},
		provider.Gitlab{},
		provider.Discord{},
		provider.Twitter{},
		provider.Twitch{},
		provider.Linkedin{},
		provider.LinkedinOIDC{},
		provider.Bitbucket{},
		provider.Dropbox{},
		provider.Microsoft{},
		provider.Notion{},
		provider.Apple{},
		provider.Line{},
		provider.Coinbase{},
		provider.Atlassian{},
		provider.Box{},
		provider.Xero{},
		// We can't provide dev credentials for Spotify, even though we register to use with their own custom credentials
		provider.Spotify{},
		provider.Slack{},
		provider.Linear{},
		provider.Expressen{},
		provider.X{},
	)

	instagram := provider.Instagram{}
	if instagram.DevClientID() != "" && instagram.DevClientSecret() != "" {
		oauth.RegisterProviders(instagram)
	}
}

// ActiveOauthConfigForProvider returns the currently active model.OauthConfig
// for the given provider, tied to the given instance pointed to by authConfig.
//
// If the provider isn't enabled for ac or no OauthConfigs could be found, it
// returns an error.
func ActiveOauthConfigForProvider(ctx context.Context, exec database.Executor, authConfigID, provider string) (*model.OauthConfig, error) {
	enabledSSOProviderRepo := repository.NewEnabledSSOProviders()
	oauthConfigRepo := repository.NewOauthConfig()

	// ensure the provider is enabled in the first place, for that
	// particular ac
	enabledSSOProvider, err := enabledSSOProviderRepo.QueryByAuthConfigIDAndProvider(ctx, exec, authConfigID, provider)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("auth_config: %w", err)
	}

	if enabledSSOProvider == nil {
		return nil, clerkerrors.NewOAuthConfigMissing(provider)
	}

	if enabledSSOProvider.UsesSharedDevConfig() {
		return model.DevOauthConfig(provider)
	}

	oauthConfig, err := oauthConfigRepo.FindByIDAndType(ctx, exec, enabledSSOProvider.ActiveOauthConfigID.String, provider)
	if err != nil {
		return nil, clerkerrors.WithStacktrace(
			"error fetch active oauth_config (provider=%s): %w", provider, err)
	}

	return oauthConfig, nil
}

// Configure enables an SSO provider in instance. To disable a provider, use
// Disable.
//
// TODO(oauth): should accept either instance (preferrably) or authConfig; not
// both.
func Configure(ctx context.Context, exec database.Executor,
	ins *model.Instance, authConfig *model.AuthConfig,
	provider oauth.Provider, clientID, clientSecret string,
	providerSettings map[string]interface{},
	additionalScopes ...string,
) (*model.OauthConfig, error) {
	newOAuthConfig := &model.OauthConfig{OauthConfig: &sqbmodel.OauthConfig{
		InstanceID:    ins.ID,
		Type:          provider.ID(),
		DefaultScopes: combineOAuthScopes(provider, additionalScopes...),
		AuthURL:       provider.AuthURL(),
		TokenURL:      provider.TokenURL(),
		ClientID:      clientID,
		ClientSecret:  clientSecret,
	}}

	var err error
	newOAuthConfig.ProviderSettings, err = json.Marshal(providerSettings)
	if err != nil {
		return nil, fmt.Errorf("sso.Configure: serialize provider settings: %w", err)
	}

	err = repository.NewOauthConfig().Insert(ctx, exec, newOAuthConfig)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("oauth_config: create %+v: %w", newOAuthConfig, err)
	}

	// in case the SSO provider was already enabled, just update it to point
	// to the newly created oauthConfig; otherwise enable it
	_, err = queries.Raw(`
INSERT INTO enabled_sso_providers (auth_config_id, provider, active_oauth_config_id) VALUES ($1, $2, $3)
ON CONFLICT (auth_config_id, provider) DO UPDATE SET active_oauth_config_id = $3;`,
		authConfig.ID, newOAuthConfig.Type, newOAuthConfig.ID).
		ExecContext(ctx, exec)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("oauth_config: create: %w", err)
	}

	return newOAuthConfig, nil
}

// Disable disables (toggles off) any provider configurations for the given
// instance (pointed to by authConfig). It is essentially the opposite of
// Configure.
func Disable(ctx context.Context, exec database.Executor,
	authConfig *model.AuthConfig, provider oauth.Provider) error {
	return repository.NewEnabledSSOProviders().DeleteByAuthConfigIDAndProvider(
		ctx, exec, authConfig.ID, provider.ID())
}

// EnableDevConfiguration enables the development SSO provider configuration,
// for the instance pointed to by ac.
func EnableDevConfiguration(ctx context.Context, exec database.Executor, ac *model.AuthConfig, provider string) error {
	repo := repository.NewEnabledSSOProviders()

	sso := &model.EnabledSSOProvider{EnabledSsoProvider: &sqbmodel.EnabledSsoProvider{
		AuthConfigID:        ac.ID,
		Provider:            provider,
		ActiveOauthConfigID: null.NewString("", false)}}

	n, err := repo.UpdateActiveOauthConfigID(ctx, exec, sso)
	if err != nil {
		return clerkerrors.WithStacktrace("user_settings: sso: %w", err)
	}
	if n > 0 {
		return nil
	}

	return repo.Insert(ctx, exec, sso)
}

// combineOAuthScopes accepts zero or more "additional" OAuth scopes, combines
// them with the base OAuth scopes for the given provider and returns them as a
// space-separated list.
func combineOAuthScopes(provider oauth.Provider, additionalScopes ...string) string {
	var scopes []string
	uniqScopes := make(map[string]bool)

	for _, s := range append(provider.BaseScopes(), additionalScopes...) {
		s = strings.TrimSpace(s)

		if uniqScopes[s] {
			continue
		}
		scopes = append(scopes, s)
		uniqScopes[s] = true
	}

	return strings.Join(scopes, " ")
}

// Given a list of OAuth scopes and a provider, return the list of non-base
// (i.e. additional) scopes.
func ExtractAdditionalOAuthScopes(provider oauth.Provider, allScopes []string) []string {
	var additional []string

	if len(allScopes) == 0 {
		return additional
	}

	baseScopes := make(map[string]bool)

	for _, s := range provider.BaseScopes() {
		baseScopes[s] = true
	}

	for _, s := range allScopes {
		if baseScopes[s] {
			continue
		}

		additional = append(additional, s)
	}

	return additional
}
