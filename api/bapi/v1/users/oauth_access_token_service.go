package users

import (
	"context"
	"errors"
	"strings"
	"time"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/sso"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/oauth"
	log "clerk/utils/log"

	"github.com/volatiletech/null/v8"
	"golang.org/x/oauth2"
)

// ListOAuthAccessTokensPaginated calls ListOAuthAccessTokens and transforms the results
// into a serialize.PaginatedResponse.
// It does not support actual pagination.
func (s *Service) ListOAuthAccessTokensPaginated(ctx context.Context, userID, providerID string) (*serialize.PaginatedResponse, apierror.Error) {
	list, apiErr := s.ListOAuthAccessTokens(ctx, userID, providerID)
	if apiErr != nil {
		return nil, apiErr
	}
	totalCount := len(list)
	data := make([]any, totalCount)
	for i, token := range list {
		data[i] = token
	}
	return serialize.Paginated(data, int64(totalCount)), nil
}

// ListOAuthAccessTokens returns valid, provider-specific OAuth access tokens tied
// to a previously authenticated user, along with the scopes it's valid for (for
// OAuth 2.0 tokens) or with its corresponding token secret (for OAuth 1.0
// tokens).
//
// If the access token we have in the database has expired, a new one will be
// issued and returned.
func (s *Service) ListOAuthAccessTokens(ctx context.Context, userID, providerID string) ([]*serialize.OAuthAccessTokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	if !strings.HasPrefix(providerID, "oauth_") {
		providerID = "oauth_" + providerID
	}

	provider, err := oauth.GetProvider(providerID)
	if err != nil {
		return nil, apierror.UnsupportedOauthProvider(providerID)
	}

	accounts, err := s.externalAccountRepo.FindAllVerifiedByUserIDAndProviderAndInstanceID(ctx, s.db, userID, provider.ID(), env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]*serialize.OAuthAccessTokenResponse, len(accounts))
	if provider.IsOAuth1() {
		for idx, account := range accounts {
			response[idx] = serialize.OAuth1AccessToken(account)
		}
	} else {
		for idx, account := range accounts {
			token, scopes, apiErr := s.getToken(ctx, account, provider.ID())
			if apiErr != nil {
				return nil, apiErr
			}

			response[idx] = serialize.OAuth2AccessToken(account, token, scopes)
		}
	}

	return response, nil
}

// package oauth2 transparently refreshes the token if it's expired. However,
// there's not a convenient way to fetch the new token.
// See https://github.com/golang/oauth2/issues/84.
func (s *Service) getToken(ctx context.Context,
	account *model.ExternalAccount, provider string,
) (string, []string, apierror.Error) {
	env := environment.FromContext(ctx)
	oauthConfig, err := sso.ActiveOauthConfigForProvider(ctx, s.db, env.AuthConfig.ID, provider)
	if err != nil {
		return "", nil, apierror.OAuthTokenProviderNotEnabled()
	}

	currentToken := oauth2.Token{
		AccessToken:  account.AccessToken,
		RefreshToken: account.RefreshToken.String,

		// if this is a zero value, the same token will be returned
		Expiry: account.AccessTokenExpiration.Time,

		// TODO(oauth): this should be persisted to and retrieved from
		// the database instead. Otherwise it's not bullet-proof since
		// some tokens may be of other types (e.g. MAC)
		TokenType: "bearer",
	}

	if !currentToken.Expiry.IsZero() {
		// provide some leeway to reduce the chance of the user receiving an
		// expired token due to clock skews. Note that package oauth2 already
		// adds a 10s leeway.
		currentToken.Expiry = currentToken.Expiry.Add(-15 * time.Minute)
	}

	config := oauthConfig.ToConfig("", account.ApprovedScopes).OAuth2
	src := config.TokenSource(ctx, &currentToken)

	// this actually goes and refreshes the token, if necessary
	newToken, err := src.Token()
	if err != nil {
		if currentToken.RefreshToken == "" {
			account.LastFailedTokenRetrievalAt = null.TimeFrom(s.clock.Now().UTC())
			if err = s.externalAccountRepo.UpdateLastFailedTokenRetrievalAt(ctx, s.db, account); err != nil {
				return "", nil, apierror.Unexpected(err)
			}
			return "", nil, apierror.OAuthMissingRefreshToken()
		}

		if strings.Contains(err.Error(), "oauth2: server response missing access_token") {
			return "", nil, apierror.OAuthMissingAccessToken()
		}

		var retrErr *oauth2.RetrieveError
		if errors.As(err, &retrErr) {
			log.Warning(ctx, err)
			return "", nil, apierror.OAuthTokenRetrievalError(retrErr)
		}

		return "", nil, apierror.Unexpected(err)
	}

	if newToken.AccessToken != currentToken.AccessToken {
		account.AccessToken = newToken.AccessToken

		if newToken.Expiry.IsZero() {
			account.AccessTokenExpiration = null.Time{}
		} else {
			account.AccessTokenExpiration = null.TimeFrom(newToken.Expiry.UTC())
		}

		if newToken.RefreshToken != "" {
			account.RefreshToken = null.StringFrom(newToken.RefreshToken)
		}

		err := s.externalAccountRepo.Update(ctx, s.db, account)
		if err != nil {
			return "", nil, apierror.Unexpected(err)
		}
	}

	return newToken.AccessToken, config.Scopes, nil
}
