package strategies

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"clerk/api/shared/sso"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cenv"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/jwt"
	"clerk/pkg/oauth"
	"clerk/pkg/rand"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/log"

	"github.com/jonboulle/clockwork"
	oauth1 "github.com/mrjones/oauth"
	"github.com/volatiletech/null/v8"
	"golang.org/x/oauth2"
)

var (
	ErrFailedExchangeCredentialsOAuth1 = errors.New("verification: failed to exchange oauth1 credentials")
	ErrSharedCredentialsNotAvailable   = errors.New("verifications: shared credentials are not available for this provider")
)

const TokenRetrievalThreshold = 24 * time.Hour

type OAuthPreparer struct {
	clock clockwork.Clock
	env   *model.Env

	prepareForm OAuthPrepareForm

	domainRepo              *repository.Domain
	externalAccountRepo     *repository.ExternalAccount
	enabledSSOProvidersRepo *repository.EnabledSSOProviders
	oauth1RequestTokensRepo *repository.OAuth1RequestTokens
	verificationRepo        *repository.Verification
}

type OAuthPrepareForm struct {
	Strategy                  string
	RedirectURL               string
	ActionCompleteRedirectURL *string
	Origin                    string
	SourceType                string
	SourceID                  string
	ClientID                  string

	// Additional scopes to request from the OAuth provider, other than those
	// defined in the instance configuration.
	AdditionalScopes []string

	// ForceConsentScreen will show the authorization consent prompt, regardless
	// of if the user has previously granted authorization, by appending
	// prompt=consent in the authorization query parameters. Currently, this is
	// only applicable to the Google provider.
	ForceConsentScreen bool

	// If provided, attach the 'login_hint' query parameter in the generated
	// authorization url in order to let the provider know about the user identity.
	// Usually it's an email address, but other identifiers (username, domain) can
	// be used as well
	LoginHint *string
}

func NewOAuthPreparer(clock clockwork.Clock, env *model.Env, prepareForm OAuthPrepareForm) OAuthPreparer {
	return OAuthPreparer{
		clock:                   clock,
		env:                     env,
		prepareForm:             prepareForm,
		domainRepo:              repository.NewDomain(),
		externalAccountRepo:     repository.NewExternalAccount(),
		enabledSSOProvidersRepo: repository.NewEnabledSSOProviders(),
		oauth1RequestTokensRepo: repository.NewOAuth1RequestTokens(),
		verificationRepo:        repository.NewVerification(),
	}
}

func (p OAuthPreparer) Identification() *model.Identification {
	return nil
}

func (p OAuthPreparer) Prepare(ctx context.Context, tx database.Tx) (*model.Verification, error) {
	clientType := client_type.FromContext(ctx)

	oauthConfig, err := sso.ActiveOauthConfigForProvider(ctx, tx, p.env.AuthConfig.ID, p.prepareForm.Strategy)
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: get OAuth config from auth config %s for strategy %s: %w",
			p.env.AuthConfig.ID, p.prepareForm.Strategy, err)
	}

	redirectURL, err := resolveRelativeURL(p.prepareForm.Origin, p.prepareForm.RedirectURL)
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: resolve relative redirect url for (%s, %s): %w", p.prepareForm.Origin, redirectURL, err)
	}

	if p.prepareForm.ActionCompleteRedirectURL != nil {
		newActionCompleteRedirectURL, err := resolveRelativeURL(p.prepareForm.Origin, *p.prepareForm.ActionCompleteRedirectURL)
		if err != nil {
			return nil, fmt.Errorf("oauth/prepare: resolve relative action complete redirect url for (%s, %s): %w",
				p.prepareForm.Origin, *p.prepareForm.ActionCompleteRedirectURL, err)
		}
		p.prepareForm.ActionCompleteRedirectURL = &newActionCompleteRedirectURL
	}

	oauthProvider, err := oauth.GetProvider(p.prepareForm.Strategy)
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: %w", err)
	}

	ssoProvider, err := p.enabledSSOProvidersRepo.FindByInstanceIDAndProvider(ctx, tx, p.env.Instance.ID, p.prepareForm.Strategy)
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: fetch provider (instance_id=%s,provider=%s): %w", p.env.Instance.ID, p.prepareForm.Strategy, err)
	}

	usesSharedDevConfig := ssoProvider.UsesSharedDevConfig()

	if usesSharedDevConfig && !oauth.DevCredentialsAvailable(oauthProvider.ID()) {
		return nil, fmt.Errorf("oauth/prepare: %w", ErrSharedCredentialsNotAvailable)
	}

	oauthFlowConfig, err := p.createOAuthFlowConfig(ctx, tx, oauthProvider, oauthConfig, usesSharedDevConfig)
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: create OAuth flow config for provider %s: %w", oauthProvider.ID(), err)
	}

	ost, err := p.createOauthStateToken(ctx, oauthConfig, oauthFlowConfig, redirectURL, clientType)
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: create OAuth state token for (%s, %s) with (%s, %v): %w",
			p.prepareForm.SourceType, p.prepareForm.SourceID, redirectURL, p.prepareForm.ActionCompleteRedirectURL, err)
	}

	claims := ost.ToOauthStateTokenClaims(p.clock)
	token, err := jwt.GenerateToken(p.env.Instance.PrivateKey, claims, p.env.Instance.KeyAlgorithm)
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: generate OAuth state token for claims %+v: %w", claims, err)
	}

	callbackDomain := p.env.Domain
	if callbackDomain.IsSatellite(p.env.Instance) {
		// callback domain should always be the primary one
		callbackDomain, err = p.domainRepo.FindByID(ctx, tx, p.env.Instance.ActiveDomainID)
		if err != nil {
			return nil, fmt.Errorf("oauth/prepare: retrieving primary domain %s for instance %s: %w",
				p.env.Instance.ActiveDomainID, p.env.Instance.ID, err)
		}
	}

	callbackURL := p.env.Instance.OauthCallbackURL(callbackDomain, usesSharedDevConfig)
	o2Config := oauthConfig.ToConfig(callbackURL, ost.ScopesRequested)
	externalAuthorizationURL, err := oauthProvider.GenerateAuthURL(o2Config, oauthFlowConfig, ost.Nonce)
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: generate auth url for provider %s: %w", ost.Provider, err)
	}

	if p.prepareForm.LoginHint != nil {
		u, err := url.Parse(externalAuthorizationURL)
		if err != nil {
			return nil, fmt.Errorf("oauth/prepare: apply login_hint parameter %s to url %s: %w", *p.prepareForm.LoginHint, externalAuthorizationURL, err)
		}

		query := u.Query()
		query.Set("login_hint", *p.prepareForm.LoginHint)
		u.RawQuery = query.Encode()

		externalAuthorizationURL = u.String()
	}

	verification, err := createVerification(ctx, tx, p.clock, &createVerificationParams{
		instanceID:               p.env.Instance.ID,
		strategy:                 p.prepareForm.Strategy,
		nonce:                    &ost.Nonce,
		token:                    &token,
		externalAuthorizationURL: &externalAuthorizationURL,
	})
	if err != nil {
		return nil, fmt.Errorf("oauth/prepare: creating verification for %s: %w", p.prepareForm.Strategy, err)
	}

	if oauthProvider.IsOAuth1() {
		// fetch a request token and persist it in our database. It will be
		// needed in the subsequent oauth callback when the user will be
		// redirected back to us by the provider
		consumer := oauth1.NewConsumer(
			oauthConfig.ClientID,
			oauthConfig.ClientSecret,
			oauth1.ServiceProvider{
				RequestTokenUrl:   oauthProvider.RequestTokenURL(),
				AuthorizeTokenUrl: oauthProvider.AuthURL(),
				AccessTokenUrl:    oauthProvider.TokenURL(),
			},
		)

		// we have to encode the JWT in the callback URL like we do in 2.0.
		//
		// Even though OAuth 1.0 does not specify a 'state' parameter like
		// 2.0 does, nevertheless any custom query parameters we provide are
		// preserved in the final callback URL.
		//
		// For consistency, we name the parameter 'state' here too, for
		// consistency with our OAuth 2.0 flow. But it could be named anything,
		// for what it's worth.
		u, err := url.Parse(callbackURL)
		if err != nil {
			return nil, fmt.Errorf("oauth/prepare: %w", err)
		}
		q := u.Query()
		q.Set("state", ost.Nonce)
		u.RawQuery = q.Encode()

		token, authurl, err := consumer.GetRequestTokenAndUrl(u.String())
		if err != nil {
			// The external package we use, doesn't wrap up the errors so we can't check the returned error type
			// e.g. errors.As(err, oauth1.HTTPExecuteError{}). For this reason we created a custom error
			return nil, fmt.Errorf("oauth/prepare: fetch request token: %v: %w", err, ErrFailedExchangeCredentialsOAuth1) //nolint:errorlint
		}

		requestToken := &model.OAuth1RequestToken{Oauth1RequestToken: &sqbmodel.Oauth1RequestToken{
			ID:         ost.Nonce,
			Token:      token.Token,
			Secret:     token.Secret,
			InstanceID: p.env.Instance.ID},
		}
		err = p.oauth1RequestTokensRepo.Insert(ctx, tx, requestToken)
		if err != nil {
			return nil, fmt.Errorf("oauth/prepare: persist request token: %w", err)
		}

		verification.ExternalAuthorizationURL = null.StringFrom(authurl)
		if err := p.verificationRepo.UpdateExternalAuthorizationURL(ctx, tx, verification); err != nil {
			return nil, fmt.Errorf("oauth/prepare: update external authorization url with %s: %w",
				authurl, err)
		}
	}

	return verification, nil
}

func (p OAuthPreparer) createOauthStateToken(ctx context.Context, oauthConfig *model.OauthConfig, oauthFlowConfig *oauth.FlowConfig, redirectURL string, clientType client_type.ClientType) (*model.OauthStateToken, error) {
	requestedScopes := set.New(oauthConfig.DefaultScopesArray()...)
	requestedScopes.Insert(p.prepareForm.AdditionalScopes...)
	clerkJSVersion := clerkjs_version.FromContext(ctx)

	nonce, err := rand.Token()
	if err != nil {
		return nil, err
	}

	return &model.OauthStateToken{
		Nonce:                     nonce,
		OauthConfigID:             oauthConfig.ID,
		SourceType:                p.prepareForm.SourceType,
		SourceID:                  p.prepareForm.SourceID,
		ClientID:                  p.prepareForm.ClientID,
		RedirectURL:               redirectURL,
		ScopesRequested:           strings.Join(requestedScopes.Array(), " "),
		Provider:                  p.prepareForm.Strategy,
		Origin:                    p.prepareForm.Origin,
		ClientType:                clientType,
		ActionCompleteRedirectURL: null.StringFromPtr(p.prepareForm.ActionCompleteRedirectURL),
		ClerkJSVersion:            clerkJSVersion,
		PKCECodeVerifier:          null.StringFromPtr(oauthFlowConfig.PKCECodeVerifier),
	}, nil
}

func (p OAuthPreparer) createOAuthFlowConfig(
	ctx context.Context,
	tx database.Tx,
	oauthProvider oauth.Provider,
	oauthConfig *model.OauthConfig,
	usesSharedDevConfig bool,
) (*oauth.FlowConfig, error) {
	opts := &oauth.FlowConfig{}

	forceConsent, err := p.shouldForceConsentScreen(ctx, tx, oauthProvider, oauthConfig, usesSharedDevConfig)
	if err != nil {
		return nil, fmt.Errorf("oauth/createOAuthFlowConfig: should force consent screen for provider %s: %w", oauthProvider.ID(), err)
	}
	opts.ForceConsentScreen = forceConsent

	if oauthProvider.UsesPKCE() {
		opts.PKCECodeVerifier = null.StringFrom(oauth2.GenerateVerifier()).Ptr()
	}

	return opts, nil
}

func (p OAuthPreparer) shouldForceConsentScreen(ctx context.Context, tx database.Tx, oauthProvider oauth.Provider, oauthConfig *model.OauthConfig, usesSharedDevConfig bool) (bool, error) {
	if p.prepareForm.ForceConsentScreen {
		return true, nil
	}
	if p.shouldForceConsentScreenDueToSharedConfig(usesSharedDevConfig, p.prepareForm.Origin) {
		return true, nil
	}
	return p.shouldForceConsentScreenDueToFailedTokenRetrieval(ctx, tx, oauthProvider, oauthConfig)
}

// To ease DX, we offer our customers shared credentials for the OAuth providers. These credentials are
// shared by every dev instance and for this reason, if a user has authorized our shared OAuth application once,
// they won't be asked to do so again, even if they are in a different instance context.
// A malicious user can leverage this by deploying a dev instance, and lead a user via an OAuth flow. Since
// the user has already authorized our shared OAuth app, they won't be asked again, and they will share their
// data with the malicious user.
func (p OAuthPreparer) shouldForceConsentScreenDueToSharedConfig(usesSharedDevConfig bool, origin string) bool {
	return usesSharedDevConfig && !isLocalhost(origin)
}

// Forcing the consent screen is the way to request a new refresh token if one is missing for Google OAuth provider.
// We force the consent screen at the instance level if the BAPI endpoint is in use and the requested scopes are the
// base scopes, as we can't detect the user before the OAuth flow.
func (p OAuthPreparer) shouldForceConsentScreenDueToFailedTokenRetrieval(ctx context.Context, tx database.Tx, oauthProvider oauth.Provider, oauthConfig *model.OauthConfig) (bool, error) {
	if !cenv.IsEnabled(cenv.FlagOAuthRefreshTokenHandlingV2) || !oauthProvider.SupportsRefreshTokenRetrieval() {
		return false, nil
	}

	threshold := p.clock.Now().UTC().Add(-1 * TokenRetrievalThreshold)
	hasFailedTokenRetrieval, err := p.externalAccountRepo.ExistsLatestFailedTokenRetrievalWithinThreshold(ctx, tx, p.env.Instance.ID, oauthProvider.ID(), threshold)
	if err != nil {
		return false, fmt.Errorf("oauth/prepare: latest failed token retrievals within the specified threshold: %w", err)
	}

	if !hasFailedTokenRetrieval {
		return false, nil
	}

	// Any outliers are captured using Sentry for further investigation.
	additionalScopes := sso.ExtractAdditionalOAuthScopes(oauthProvider, oauthConfig.DefaultScopesArray())
	if len(additionalScopes) != 0 {
		log.Warning(ctx, "oauth/prepare: cannot force consent screen for instance %s and additional scopes: %v",
			p.env.Instance.ID, additionalScopes)
		return false, nil
	}

	return true, nil
}

func isLocalhost(input string) bool {
	u, err := url.ParseRequestURI(input)
	if err != nil {
		return false
	}

	return u.Hostname() == "localhost"
}
