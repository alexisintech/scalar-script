package oauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/clients"
	"clerk/api/fapi/v1/cookies"
	"clerk/api/fapi/v1/external_account"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/environment"
	"clerk/api/shared/events"
	"clerk/api/shared/identifications"
	"clerk/api/shared/restrictions"
	"clerk/api/shared/saml"
	"clerk/api/shared/sentryenv"
	"clerk/api/shared/serializable"
	"clerk/api/shared/sessions"
	"clerk/api/shared/sign_in"
	"clerk/api/shared/sign_up"
	userlockout "clerk/api/shared/user_lockout"
	"clerk/api/shared/users"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cache"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/client_type"
	ctxenv "clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/emailaddress"
	"clerk/pkg/jwt"
	"clerk/pkg/oauth"
	"clerk/pkg/rand"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/unverifiedemails"
	usersettings "clerk/pkg/usersettings/clerk"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/form"
	"clerk/utils/log"
	"clerk/utils/param"
	pkiutils "clerk/utils/pki"
	"clerk/utils/response"
	urlUtils "clerk/utils/url"

	oauth1 "github.com/mrjones/oauth"
	"github.com/volatiletech/null/v8"
)

// OAuth implements the OAuth 1.0a and 2.0 callback flows.
type OAuth struct {
	deps clerk.Deps

	// services
	environmentService     *environment.Service
	eventService           *events.Service
	externalAccountService *external_account.Service
	restrictionService     *restrictions.Service
	serializableService    *serializable.Service
	signInService          *sign_in.Service
	signUpService          *sign_up.Service
	verificationService    *verifications.Service
	userLockoutService     *userlockout.Service
	userService            *users.Service
	clientService          *clients.Service
	sessionService         *sessions.Service
	clientDataService      *client_data.Service

	// repositories
	accountTransfersRepo    *repository.AccountTransfers
	allowlistRepo           *repository.Allowlist
	domainRepo              *repository.Domain
	enabledSSOProviderRepo  *repository.EnabledSSOProviders
	externalAccountRepo     *repository.ExternalAccount
	identificationRepo      *repository.Identification
	instanceRepo            *repository.Instances
	oauth1RequestTokensRepo *repository.OAuth1RequestTokens
	oauthConfigRepo         *repository.OauthConfig
	redirectUrlsRepo        *repository.RedirectUrls
	samlService             *saml.SAML
	signInRepo              *repository.SignIn
	signUpRepo              *repository.SignUp
	userRepo                *repository.Users
	verificationRepo        *repository.Verification
}

func New(deps clerk.Deps) *OAuth {
	return &OAuth{
		deps:                   deps,
		environmentService:     environment.NewService(),
		eventService:           events.NewService(deps),
		externalAccountService: external_account.NewService(deps),
		restrictionService:     restrictions.NewService(deps.EmailQualityChecker()),
		serializableService:    serializable.NewService(deps.Clock()),
		signInService:          sign_in.NewService(deps),
		signUpService:          sign_up.NewService(deps),
		verificationService:    verifications.NewService(deps.Clock()),
		userLockoutService:     userlockout.NewService(deps),
		userService:            users.NewService(deps),
		clientService:          clients.NewService(deps),
		sessionService:         sessions.NewService(deps),
		clientDataService:      client_data.NewService(deps),

		accountTransfersRepo:    repository.NewAccountTransfers(),
		allowlistRepo:           repository.NewAllowlist(),
		domainRepo:              repository.NewDomain(),
		enabledSSOProviderRepo:  repository.NewEnabledSSOProviders(),
		externalAccountRepo:     repository.NewExternalAccount(),
		identificationRepo:      repository.NewIdentification(),
		instanceRepo:            repository.NewInstances(),
		oauth1RequestTokensRepo: repository.NewOAuth1RequestTokens(),
		oauthConfigRepo:         repository.NewOauthConfig(),
		redirectUrlsRepo:        repository.NewRedirectUrls(),
		samlService:             saml.New(),
		signInRepo:              repository.NewSignIn(),
		signUpRepo:              repository.NewSignUp(),
		userRepo:                repository.NewUsers(),
		verificationRepo:        repository.NewVerification(),
	}
}

// POST /v1/oauth_callback
//
// Converts a POST to a GET, because if it's a POST coming from an external
// origin (e.g. from Apple), we won't have access to the long-lived cookie since
// it's SameSite=Lax.
//
// By redirecting from our own origin (e.g. clerk.foo.com) with a 303, we're
// effectively converting the CrossSite POST to a SameSite GET. This will result
// in us having access to our cookies.
//
// This is necessary because some providers (e.g. Apple) issue a POST instead of
// a GET. The flow in such a scenario is this:
//
//	Apple origin -> POST /v1/oauth_callback -> GET /v1/oauth_callback
func (o *OAuth) ConvertToGET(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := r.ParseForm()
	if err != nil {
		return nil, apierror.MalformedRequestParameters(err)
	}

	dest, err := url.Parse(r.URL.Path)
	if err != nil {
		// r.URL.Path is a path already parsed and matched to this handler by
		// our router, so this should never happen
		panic(err)
	}

	q := dest.Query()
	for k := range r.PostForm {
		q.Set(k, r.PostForm.Get(k))
	}
	dest.RawQuery = q.Encode()

	http.Redirect(w, r, dest.String(), http.StatusSeeOther)

	return nil, nil
}

// SetEnvironmentFromStateParam sets model.Env in r.Context(), based on the
// 'state' parameter we received from an OAuth callback.
//
// Only used in development instances.
func (o *OAuth) SetEnvironmentFromStateParam(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()

	verification, err := o.verificationFromStateParam(ctx, r)
	if err != nil {
		return r.WithContext(ctx), apierror.InvalidAuthorization()
	}

	env, err := o.environmentService.Load(ctx, o.deps.DB(), verification.InstanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return r.WithContext(ctx), apierror.InvalidHost()
	} else if err != nil {
		return r.WithContext(ctx), apierror.Unexpected(err)
	}

	log.AddToLogLine(ctx, log.InstanceID, env.Instance.ID)
	log.AddToLogLine(ctx, log.EnvironmentType, env.Instance.EnvironmentType)
	log.AddToLogLine(ctx, log.DomainName, env.Domain.Name)

	sentryenv.EnrichScope(ctx, env)

	ctx = ctxenv.NewContext(ctx, env)

	return r.WithContext(ctx), nil
}

// Callback is the handler that serves the OAuth 1.0a/2.0 callback requests.
// *******************************************************
// *					  OAuth 2.0 FLOW                           *
// *******************************************************
//
// PRE-CALLBACK (before this endpoint):
// 1. check that the verification is still valid
// 2. create authorization link for the provider and return it to the frontend
// 3. frontent redirects to the authorization link
// 4. user authenticates/authorizes on provider's site
// 5. provider redirects here
//
// CALLBACK (you're here now):
//  1. find OauthStateToken based on the state param returned
//  2. make sure it and the verification it's on are still valid
//  3. save exchange_code and returned_scopes on oauth_state_token
//  4. create an "ExternalAccountCreator" (holds the newly authenticated account data from the oauth_provider)
//  5. process ExternalAccountCreator with regard to the SignIn/SignUp and the Verification it's coming from.
//  6. redirect to redirect_url or action_complete_redirect_url. If this is a native application flow, return
//     the client.rotating_token_nonce in the whitelisted redirect_url in order to be used by the native client
//     to update its `__client` jwt.
//
// *******************************************************
// *					  OAuth 1.0 FLOW                           *
// *******************************************************
//
// PRE-CALLBACK (before this endpoint):
// 1. check that the verification is still valid
// 2. fetch a request token from the provider and persist it to the database
// 3. create authorization link for the provider and provide it to the frontend
// 4. frontent redirects to the authorization link
// 5. user authenticates/authorizes on provider's site
// 6. provider redirects here
//
// CALLBACK (you're here now):
//  1. find OauthStateToken based on the state param returned
//  2. make sure it and the verification it's on are still valid
//  3. provide the request token from PRE-CALLBACK step (2), in exchange
//     for an access token
//  4. create an ExternalAccountCreator and use the access token to fetch
//     the user data from the provider
//  5. process ExternalAccountCreator with regard to the SignIn/SignUp and
//     the Verification it's coming from.
//  6. redirect to redirect_url or action_complete_redirect_url. If this is a native application flow, return
//     the client.rotating_token_nonce in the whitelisted redirect_url in order to be used by the native client
//     to update its `__client` jwt.
func (o *OAuth) Callback(w http.ResponseWriter, r *http.Request) (_ interface{}, retErr apierror.Error) {
	ctx := r.Context()
	env := ctxenv.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	db := o.deps.DB()

	// OAuth callback is restricted only to primary domains
	if env.Domain.IsSatellite(env.Instance) {
		return nil, apierror.OperationNotAllowedOnSatelliteDomain()
	}

	ver, err := o.verificationFromStateParam(ctx, r)
	if err != nil {
		return nil, apierror.InvalidAuthorization()
	}

	oauthProvider, err := oauth.GetProvider(ver.Strategy)
	if err != nil {
		return nil, apierror.UnsupportedOauthProvider(ver.Strategy)
	}

	// handle errors from OAuth providers
	if r.URL.Query().Get("error") == apierror.OAuthRedirectURIMismatch {
		return nil, apierror.OAuthInvalidRedirectURI(oauthProvider.Name())
	}

	ost, err := o.oauthStateTokenFromVerification(ctx, ver, env.Instance.ID)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			log.Warning(ctx, "OAuthStateToken JWT expired")
		} else if !errors.Is(err, ErrMismatchedClientID) && !errors.Is(err, jwt.ErrInvalidSignature) {
			sentryclerk.CaptureException(ctx, err)
		}
		return nil, apierror.InvalidAuthorization()
	}

	// Reject the callback for sign-up or sign-in if provider is no longer authenticatable
	if (ost.SourceType == constants.OSTSignIn || ost.SourceType == constants.OSTSignUp) &&
		!userSettings.AuthenticatableSocialStrategies().Contains(ver.Strategy) {
		return nil, apierror.NonAuthenticatableOauthProvider(ver.Strategy)
	}

	// keep the ClerkJS version in the context for the duration of the request
	ctx = clerkjs_version.NewContext(ctx, ost.ClerkJSVersion)
	// TODO(dimkl): Move it to Preparer : keep the Client type in the context for the duration of the request
	ctx = client_type.NewContext(ctx, ost.ClientType)

	callback, apierr := validateOauthForm(oauthProvider, r.Form)
	if apierr != nil {
		return nil, apierr
	}

	if callback.Scope == nil && callback.Error == nil {
		// The provider MAY NOT include the "scope" parameter in case the issued
		// access token's scopes were the same as the ones that were initially
		// requested.
		//
		// In that case, we assume that we were granted access to all
		// the scopes we requested.
		//
		// See https://datatracker.ietf.org/doc/html/rfc6749#section-3.3).
		callback.Scope = &ost.ScopesRequested
	}

	// "consume" the oauth_state_token and "attempt" the verification first, so
	// that this OauthStateToken can only be used once
	ver.Attempts++
	err = o.verificationRepo.UpdateAttempts(ctx, db, ver)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// NOTE: this is security-critical in order to mitigate replay attacks of OAuth state.
	// Since callbacks can be called at most once, we have to return a 403 error, as something has gone wrong.
	if status, err := o.verificationService.Status(ctx, db, ver); err != nil {
		return nil, apierror.Unexpected(err)
	} else if status == constants.VERExpired {
		// On expiration redirect to the Accounts page so that the user can resume the flow.
		// TODO: inform user that the verification has expired, see:
		//       https://linear.app/clerk/issue/AUTH-552/when-oauthstatetoken-ost-expires-return-the-error-to-clerkjs-ui
		http.Redirect(w, r, ost.RedirectURL, http.StatusSeeOther)
		return nil, nil
	} else if status == constants.VERVerified {
		// If the status is Verified, the external account is already verified,
		// and we can redirect to the redirect URL and rely on the existing session.
		// This can happen if the user goes back in the browser and re-authenticates
		// via the OAuth provider, leading to a new callback with the same state.
		redirectURL := determineRedirectURL(ost, redirectURLOptions{checkSession: false})
		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
		return nil, nil
	} else if status != constants.VERUnverified {
		// If the status is Failed, this means that we exceeded the attempts threshold (only one attempt is allowed).
		return nil, apierror.InvalidAuthorization()
	}

	//
	// if we have an error on return past this point, instead of responding w/ it,
	// update the verification obj. with the errorType and then re-direct,
	//
	var client *model.Client
	var createdSession *model.Session
	var samlRedirectURL *string
	defer func() {
		redirectURL := determineRedirectURL(ost, redirectURLOptions{
			checkSession: true,
			session:      createdSession,
			samlURL:      samlRedirectURL,
		})

		// SECURITY: Inject a nonce that will allow a subsequent request from this client to be
		// accepted, even though it doesn't have the proper rotating token. This enables OAuth flows
		// across different outlets (i.e. native apps). More info at
		// https://www.notion.so/clerkdev/OAuth-authentication-across-different-outlets-202d682518be463ab0d335ce4529aae9
		if client != nil && client.RotatingTokenNonce.Valid {
			exists, err := o.isRedirectURLAllowed(ctx, db, env.Instance, redirectURL)
			if err != nil {
				retErr = apierror.Unexpected(err)
			} else if exists {
				redirectURL, err = urlUtils.AddQueryParameters(
					redirectURL,
					urlUtils.NewQueryStringParameter(param.RotatingTokenNonce.Name, client.RotatingTokenNonce.String),
				)
				if err != nil {
					retErr = apierror.Unexpected(err)
				}
			} else {
				retErr = apierror.RedirectURLMismatch(redirectURL)
			}
		}

		if retErr != nil {
			sentryclerk.CaptureException(ctx, clerkerrors.WithStacktrace(
				"oauth: error during oauth callback for verification %s: %w", ver.ID, retErr))

			// add error to verification obj
			verErr := apierror.Unexpected(nil)
			if !apierror.IsInternal(retErr) {
				verErr = retErr
			}

			resp := apierror.ToResponse(ctx, verErr)
			jsonBytes, err := json.Marshal(resp.Errors[0])
			if err != nil {
				retErr = apierror.Unexpected(err)
				return
			}

			// update verification with error
			ver.Error = null.JSONFrom(jsonBytes)
			if err := o.verificationRepo.UpdateError(ctx, db, ver); err != nil {
				retErr = apierror.Unexpected(err)
				return
			}

			retErr = nil
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
			return
		}

		if createdSession != nil {
			// If not an action complete redirect url, add the created_session_id query parameter.
			if !ost.ActionCompleteRedirectURL.Valid {
				var checkErr error
				redirectURL, checkErr = urlUtils.AddQueryParameters(redirectURL, urlUtils.NewQueryStringParameter("created_session_id", createdSession.ID))
				if checkErr != nil {
					retErr = apierror.Unexpected(checkErr)
					return
				}
			}

			csrfToken, err := response.SetCSRFToken(w, env.Domain.Name)
			if err != nil {
				retErr = apierror.Unexpected(err)
				return
			}

			handshakeRedirectURL, handshakeError := o.clientService.SetHandshakeTokenInResponse(ctx, w, client, redirectURL, ost.ClientType, ost.ClerkJSVersion)
			if handshakeError != nil {
				retErr = handshakeError
				return
			}

			if env.Instance.IsDevelopmentOrStaging() {
				respondWithNewClientForDevelopment(w, r, env.Domain, client, csrfToken, handshakeRedirectURL)
				return
			}

			redirectWithNewClientForProduction(w, r, db, o.deps.Cache(), env.Domain, client, handshakeRedirectURL)
			return
		}

		http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	}() // defer

	if callback.Error == nil && isOAuth2(oauthProvider) {
		ost.ScopesReturned = *callback.Scope
		ost.OauthExchangeCode = null.StringFromPtr(callback.Code)
	}

	if callback.Error != nil {
		return nil, apierror.OAuthAccessDenied(oauthProvider.Name())
	}

	oauthConfig, err := o.getOAuthConfigFromOAuthStateToken(ctx, ost, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// In OAuth 1.0, we have to exchange the request token (that we fetched
	// before redirecting to the provider) for an access token.
	if oauthProvider.IsOAuth1() {
		ost.OAuth1AccessToken, err = o.fetchOAuth1AccessToken(
			ctx, db, ost, oauthProvider,
			env.Instance.ID, oauthConfig.ClientID, oauthConfig.ClientSecret,
			r.FormValue(oauth1.TOKEN_PARAM), r.FormValue(oauth1.VERIFIER_PARAM),
		)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	ssoProvider, err := o.enabledSSOProviderRepo.QueryByInstanceIDAndProvider(ctx, db, env.Instance.ID, ver.Strategy)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if ssoProvider == nil {
		return nil, apierror.UnsupportedOauthProvider(oauthProvider.Name())
	}

	callbackDomain := env.Domain
	if callbackDomain.IsSatellite(env.Instance) {
		// callback domain should always be the primary one
		callbackDomain, err = o.domainRepo.FindByID(ctx, db, env.Instance.ActiveDomainID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	callbackURL := env.Instance.OauthCallbackURL(callbackDomain, ssoProvider.UsesSharedDevConfig())
	customOAuthConfig := oauthConfig.ToConfig(callbackURL, ost.ScopesReturned)

	oauthUser, err := external_account.FetchUser(ctx, oauthProvider, customOAuthConfig, ost, userSettings, r.Form)
	if err != nil {
		var tokExErr oauth.TokenExchangeError
		if errors.As(err, &tokExErr) {
			log.Warning(ctx, err)
			return nil, apierror.OAuthTokenExchangeError(err)
		}

		var fetchUserErr oauth.FetchUserError
		if errors.As(err, &fetchUserErr) {
			log.Warning(ctx, err)
			return nil, apierror.OAuthFetchUserError(err)
		}

		return nil, apierror.Unexpected(err)
	}

	var apiErr apierror.Error
	samlRedirectURL, apiErr = o.handleSAML(ctx, env, ost, oauthUser)
	if apiErr != nil {
		return nil, apiErr
	}
	if samlRedirectURL != nil {
		// We have to redirect to the SAML IdP in order to complete the flow. We can't redirect directly
		// from this place as defer will take over. We have to do it within the defer function instead.
		return nil, nil
	}

	var finishError error

	//
	// start the main transaction
	//
	var (
		toSignUpAccountTransfer *model.AccountTransfer
		toSignInAccountTransfer *model.AccountTransfer
	)
	txErr := db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		switch ost.SourceType {
		case constants.OSTSignIn:
			signIn, err := o.signInRepo.QueryByIDAndInstance(ctx, tx, ost.SourceID, env.Instance.ID)
			if err != nil {
				return true, apierror.Unexpected(err)
			} else if signIn == nil {
				return true, apierror.InvalidClientStateForAction("a get", "No sign_in.")
			}

			iClient, repoErr := o.clientDataService.FindClient(ctx, env.Instance.ID, signIn.ClientID)
			if repoErr != nil {
				if errors.Is(repoErr, client_data.ErrNoRecords) {
					return true, apierror.ClientNotFound(signIn.ClientID)
				}
				return true, apierror.Unexpected(err)
			}
			client = iClient.ToClientModel()

			createdSession, toSignUpAccountTransfer, finishError = o.finishOauthForSignIn(
				ctx,
				tx,
				env,
				client,
				signIn,
				ver,
				ost,
				oauthUser,
			)
		case constants.OSTSignUp:
			signUp, err := o.signUpRepo.QueryByIDAndInstance(ctx, db, ost.SourceID, env.Instance.ID)
			if err != nil {
				return true, err
			} else if signUp == nil {
				return true, apierror.InvalidClientStateForAction("a get", "No sign_up.")
			}

			iClient, err := o.clientDataService.FindClient(ctx, env.Instance.ID, signUp.ClientID)
			if err != nil {
				if errors.Is(err, client_data.ErrNoRecords) {
					return true, apierror.ClientNotFound(signUp.ClientID)
				}
				return true, apierror.Unexpected(err)
			}
			client = iClient.ToClientModel()

			createdSession, toSignInAccountTransfer, finishError = o.finishOauthForSignUp(
				ctx,
				tx,
				env,
				client,
				signUp,
				ver,
				ost,
				oauthUser,
			)
		case constants.OSTOAuthConnect:
			finishError = o.finishConnectOauthAccount(ctx, tx, env.Instance, userSettings, ver, ost, oauthUser)
		case constants.OSTOAuthReauthorize:
			finishError = o.finishOAuthForReauthorize(ctx, tx, env.Instance, userSettings, ost, oauthUser)
		default:
			return true, fmt.Errorf("OauthStateToken SourceType not implemented: %s", ost.SourceType)
		}

		// We don't want to rollback the transaction on known API errors,
		// instead we want to proceed and communicate them to the frontend.
		// However, if we encountered a database error the transaction is in failed state
		// and any attempt to commit will raise further errors from the DB driver.
		if apiErr, ok := apierror.As(finishError); ok {
			rollbackTx := apierror.CauseMatches(apiErr, func(cause error) bool {
				return clerkerrors.IsUniqueConstraintViolation(cause, clerkerrors.UniqueIdentification)
			})
			return rollbackTx, finishError
		}
		if finishError != nil {
			return true, finishError
		}
		return false, nil
	})
	if txErr != nil {
		apiErr, ok := apierror.As(txErr)
		if ok {
			if err := o.updateClientForAccountTransfer(ctx, client, toSignUpAccountTransfer, toSignInAccountTransfer); err != nil {
				return nil, apierror.Unexpected(err)
			}
			return nil, apiErr
		}
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueReservedIdentification) ||
			clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueIdentification) {
			return nil, apierror.IdentificationClaimed()
		}
		return nil, apierror.Unexpected(txErr)
	}

	if createdSession != nil {
		if err := o.sessionService.Activate(ctx, env.Instance, createdSession); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	return nil, nil // redirect logic happens in defer
}

// provider-agnostic validations of the callback request
func validateOauthForm(provider oauth.Provider, params url.Values) (*oauth.Callback, apierror.Error) {
	errorFormVal := form.GetNullString(params, param.Error.Name)
	var errorVal *string
	if errorFormVal != nil {
		errorVal = &errorFormVal.String
	}

	paramSet := param.NewSet(param.State)

	if errorVal == nil {
		if isOAuth2(provider) {
			paramSet.Add(param.Code)
		} else if provider.IsOAuth1() {
			paramSet.Add(param.OAuthToken, param.OAuthVerifier)
		}
	}

	if formErrs := form.CheckRequiredOnly(params, paramSet); formErrs != nil {
		return nil, apierror.InvalidOAuthCallback()
	}

	stateFormVal := form.GetNullString(params, param.State.Name)
	var stateVal *string
	if stateFormVal != nil {
		stateVal = &stateFormVal.String
	}

	codeFormVal := form.GetNullString(params, param.Code.Name)
	var codeVal *string
	if codeFormVal != nil {
		codeVal = &codeFormVal.String
	}

	scopeFormVal := form.GetNullString(params, param.Scope.Name)
	var scopeVal *string
	if scopeFormVal != nil {
		scopeVal = &scopeFormVal.String
	}

	return &oauth.Callback{
		State: stateVal,
		Code:  codeVal,
		Scope: scopeVal,
		Error: errorVal,
	}, nil
}

// SignIn Flow for oauth:
// 1.
// if [User not found] AND [SignIn not identified]
// -- [Allow a Transfer to a SignUp obj.]
// ---- Create ExternalAccount (w/ LinkedAccount)
// ---- Create AccountTransfer, add ExternalAccount, put a direction on it, [sign-in]-->[sign-up] and put it on the client
// ---- Put error on the verification object
// else if [ExternalAccount Ident not found] AND [SignIn identified]
// -- [Error, external account doesn't match the account on the SignIn]
// end
//
// 2.
// if [User already signed in]
// -- [Error, already signed in]
// end
//
// 3.
// if [User found] AND [SignIn not identified] // [complete first_factor of sign_in]
// ---- if [ExternalAccount Found]
// -------- Update the externalAccount with returned data
// ---- else
// -------- Create externalAccount w/ links
// ---- end
// ---- Attach Identification to the SignIn
// ---- Update Verification on the SignIn
// ---- if the SignIn is now valid, create session
// end
//
// 4.
// if [ExternalAccount found] AND [SignIn Identified]
// ---- if [ExternalAccount Identification] != [SignIn Identification]
// ---- - [Error, invalid account for SignIn]
// ---- else
// ---- - [account matches, verify the first factor of the SignIn w/ the ExternalAccount]
// ---- ---- Update the externalAccount with returned data
// ---- ---- Update Verification on the SignIn
// ---- ---- if the SignIn is now valid, create session
// ---- end
// end
//
// returns *Session: if a new session was created, otherwise nil
//
//	error: any error that occurred.
func (o *OAuth) finishOauthForSignIn(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	client *model.Client,
	signIn *model.SignIn,
	ver *model.Verification,
	ost *model.OauthStateToken,
	oauthUser *oauth.User,
) (*model.Session, *model.AccountTransfer, error) {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	apiErr := o.isEmailAddressAllowed(ctx, tx, oauthUser.EmailAddress, userSettings, ver.Strategy, env.Instance.ID, env.AuthConfig.TestMode)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	extAccIdent, err := o.identificationRepo.QueryLatestClaimedByInstanceAndTypeAndProviderUserID(ctx, tx, env.Instance.ID, oauthUser.ProviderUserID, oauthUser.ProviderID)
	if err != nil {
		return nil, nil, err
	}

	emailIdent, err := o.identificationRepo.QueryClaimedVerifiedOrReservedByInstanceAndIdentifierAndTypePrioritizingVerified(ctx, tx, env.Instance.ID, oauthUser.EmailAddress, constants.ITEmailAddress)
	if err != nil {
		return nil, nil, err
	}

	userExists, existingUserID := o.findExistingUserIDForSignIn(oauthUser, extAccIdent, emailIdent)

	// 1.
	// if [User not found]
	// -- [Allow a Transfer to a Service obj.]
	// ---- Create ExternalAccount (w/ LinkedAccount)
	// ---- if email not verified but same email exists
	// ------ Start a verification process to potentially link to existing user
	// ---- else
	// ------ Create AccountTransfers, add ExternalAccount, put a direction on it, [sign-in]-->[sign-up] and put it on the client
	// ------ Put error on the verification object
	// else if [ExternalAccount Ident not found] AND [SignIn identified]
	// -- [Error, external account doesn't match the account on the SignIn]
	// end
	//
	if !userExists {
		if extAccIdent == nil {
			creationResult, err := o.externalAccountService.CreateAndLink(ctx, tx, ver, ost, oauthUser, env.Instance, nil, userSettings)
			if err != nil {
				return nil, nil, err
			}

			extAccIdent = creationResult.Identification
		} else {
			_, err := o.externalAccountService.Update(ctx, tx, userSettings, ost, oauthUser, env.Instance)
			if err != nil {
				return nil, nil, err
			}
		}

		// In case the email address is not verified, but user with the same email address exists,
		// we want to start a verification process to potentially link the external account to the existing user.
		// So we don't want to proceed to account transfer in this case.
		if !oauthUser.EmailAddressVerified &&
			identifications.FindUserIDIfExists(emailIdent) != nil &&
			unverifiedemails.IsVerifyFlowSupportedByClerkJSVersion(ctx) {
			signIn.IdentificationID = null.StringFrom(emailIdent.ID)
			signIn.ToLinkIdentificationID = null.StringFrom(extAccIdent.ID)

			err = o.signInRepo.Update(ctx, tx, signIn,
				sqbmodel.SignInColumns.IdentificationID, sqbmodel.SignInColumns.ToLinkIdentificationID)
			return nil, nil, err
		}

		accTransfer := model.AccountTransfer{AccountTransfer: &sqbmodel.AccountTransfer{
			InstanceID:       env.Instance.ID,
			IdentificationID: extAccIdent.ID,
			ExpireAt:         o.deps.Clock().Now().UTC().Add(time.Second * time.Duration(constants.ExpiryTimeTransactional)),
		}}
		err := o.accountTransfersRepo.Insert(ctx, tx, &accTransfer)
		if err != nil {
			return nil, nil, err
		}

		// add transfer obj to verification
		ver.AccountTransferID = null.StringFrom(accTransfer.ID)
		if err = o.verificationRepo.UpdateAccountTransferID(ctx, tx, ver); err != nil {
			return nil, nil, err
		}

		return nil, &accTransfer, apierror.ExternalAccountNotFound()
	}

	// 2.
	// if [User already signed in, for single session instances]
	// -- [Error, already signed in]
	// end
	//
	// Check if currently logged in and enforce single-session restriction.
	// Important note: the (read-only) check happens outside of the in-progress database transaction,
	// however breaking transaction isolation here may also be considered desirable.
	if userSession, err := o.checkSingleSessionMode(ctx, env, client, *existingUserID); err != nil {
		return nil, nil, err
	} else if userSession != nil {
		return nil, nil, apierror.AlreadySignedIn(userSession.ID)
	}

	// 3.
	// if [User found] // [complete first_factor of sign_in]
	// ---- if [ExternalAccount Not Found]
	// -------- Create ExternalAccount w/ links
	// ---- else
	// -------- Update the ExternalAccount with returned data
	// ---- end
	// ---- Attach Identification to the SignIn
	// ---- Update Verification on the SignIn
	// ---- if the SignIn is now valid, create session
	// end
	//

	externalAccountExists := extAccIdent != nil

	if !externalAccountExists {
		creationResult, err := o.externalAccountService.CreateAndLink(ctx, tx, ver, ost, oauthUser, env.Instance, existingUserID, userSettings)
		if err != nil {
			return nil, nil, err
		}

		extAccIdent = creationResult.Identification
	} else {
		_, err := o.externalAccountService.Update(ctx, tx, userSettings, ost, oauthUser, env.Instance)
		if err != nil {
			return nil, nil, err
		}
	}

	signIn.IdentificationID = null.StringFrom(extAccIdent.ID)
	cols := []string{sqbmodel.SignInColumns.IdentificationID}

	// If the verification is required, we're forcing the user to verify the email address
	// before proceeding with the sign-in with first factor.
	skipFirstFactor := true
	if extAccIdent.RequiresVerification.Bool {
		signIn.IdentificationID = null.StringFrom(emailIdent.ID)
		signIn.ToLinkIdentificationID = null.StringFrom(extAccIdent.ID)
		cols = append(cols, sqbmodel.SignInColumns.ToLinkIdentificationID)
		skipFirstFactor = false
	}

	if err := o.signInRepo.Update(ctx, tx, signIn, cols...); err != nil {
		return nil, nil, err
	}

	user, err := o.userRepo.FindByIDAndInstance(ctx, tx, extAccIdent.UserID.String, env.Instance.ID)
	if err != nil {
		return nil, nil, err
	}

	apiErr = o.userLockoutService.EnsureUserNotLocked(ctx, tx, env, user)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	if err = o.signInService.AttachFirstFactorVerification(ctx, tx, signIn, ver.ID, skipFirstFactor); err != nil {
		return nil, nil, err
	}

	if err = o.userService.SyncSignInPasswordReset(ctx, tx, env.Instance, signIn, user); err != nil {
		return nil, nil, err
	}

	readyToConvert, err := o.signInService.IsReadyToConvert(ctx, tx, signIn, userSettings)
	if err != nil {
		return nil, nil, err
	}

	if readyToConvert {
		// Trigger a 'user.updated' event only if a new external account has been created
		if !externalAccountExists {
			if err = o.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, user); err != nil {
				return nil, nil, fmt.Errorf("finishOauthForSignIn: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err)
			}
		}

		rotatingTokenNonce, err := generateRotatingTokenNonce(ost)
		if err != nil {
			return nil, nil, fmt.Errorf("finishOauthForSignIn: generating rotating_token_nonce for client %s: %w", client.ID, err)
		}

		us, err := o.signInService.ConvertToSession(
			ctx,
			tx,
			sign_in.ConvertToSessionParams{
				Client:             client,
				Env:                env,
				SignIn:             signIn,
				User:               user,
				RotatingTokenNonce: rotatingTokenNonce,
			},
		)
		if err != nil {
			return nil, nil, err
		}

		return us, nil, nil
	}

	return nil, nil, nil
}

// findExistingUserIDForSignIn locates a user ID by their external account or email identifications, depending on
// whether the OAuth email is verified or not.
func (o *OAuth) findExistingUserIDForSignIn(oauthUser *oauth.User, extAccIdent *model.Identification, emailIdent *model.Identification) (bool, *string) {
	var existingUserID *string
	if oauthUser.EmailAddressVerified {
		existingUserID = identifications.FindUserIDIfExists(extAccIdent, emailIdent)
	} else {
		existingUserID = identifications.FindUserIDIfExists(extAccIdent)
	}
	return existingUserID != nil, existingUserID
}

// - SignUp
// if externalID currently logged in || len(linkedIdentifications)>0 currently logged in
// ---- throw error
// else if externalID exists on a different account || len(linkedIdentifications)>0 exists on a different account
// ---- allow transfer
// else if externalID is on this SignUp  || len(linkedIdentifications)>0 is on this signUp
// ---- update the externalAccount
// else (externalID not found)  && len(linkedIdentifications) == 0 not found
// ---- create the new externalAccount and attach it. all we need to do is set the right IDs
// end
func (o *OAuth) finishOauthForSignUp(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	client *model.Client,
	signUp *model.SignUp,
	ver *model.Verification,
	ost *model.OauthStateToken,
	oauthUser *oauth.User,
) (*model.Session, *model.AccountTransfer, error) {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	apiErr := o.isEmailAddressAllowed(
		ctx,
		tx,
		oauthUser.EmailAddress,
		userSettings,
		ver.Strategy,
		env.Instance.ID,
		env.AuthConfig.TestMode,
	)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	extAccIdent, err := o.identificationRepo.QueryLatestClaimedByInstanceAndTypeAndProviderUserID(ctx, tx, env.Instance.ID, oauthUser.ProviderUserID, oauthUser.ProviderID)
	if err != nil {
		return nil, nil, err
	}

	emailIdent, err := o.identificationRepo.QueryClaimedVerifiedOrReservedByInstanceAndIdentifierAndTypePrioritizingVerified(ctx, tx, env.Instance.ID, oauthUser.EmailAddress, constants.ITEmailAddress)
	if err != nil {
		return nil, nil, err
	}

	userExists, existingUserID := o.findExistingUserIDForSignUp(ctx, oauthUser, extAccIdent, emailIdent)

	var externalAccount *model.ExternalAccount

	if userExists {
		// load user to check if they are locked
		user, err := o.userRepo.FindByIDAndInstance(ctx, tx, *existingUserID, env.Instance.ID)
		if err != nil {
			return nil, nil, err
		}

		apiErr := o.userLockoutService.EnsureUserNotLocked(ctx, tx, env, user)
		if apiErr != nil {
			return nil, nil, apiErr
		}

		// since the user exists via a linked account, -- create the external account,
		// and then create the transfer with it
		//
		if extAccIdent == nil {
			// we only want to do account linking right now, if the email address is verified.
			var toLinkUserID *string
			if oauthUser.EmailAddressVerified {
				toLinkUserID = existingUserID
			}

			creationResult, err := o.externalAccountService.CreateAndLink(ctx, tx, ver, ost, oauthUser, env.Instance, toLinkUserID, userSettings)
			if err != nil {
				return nil, nil, err
			}

			extAccIdent = creationResult.Identification
		}

		// Check if currently logged in and enforce single-session restriction.
		// Important note: the (read-only) check happens outside of the in-progress database transaction,
		// however breaking transaction isolation here may also be considered desirable.
		if userSession, err := o.clientActiveUserSession(ctx, env, client, *existingUserID); err != nil {
			return nil, nil, err
		} else if userSession != nil {
			return nil, nil, apierror.AlreadySignedIn(userSession.ID)
		}

		accTransfer := model.AccountTransfer{AccountTransfer: &sqbmodel.AccountTransfer{
			InstanceID: env.Instance.ID,
			ExpireAt:   o.deps.Clock().Now().UTC().Add(time.Second * time.Duration(constants.ExpiryTimeTransactional)),
		}}

		// set IDs for account linking based on whether the external account is linked to a user.
		if extAccIdent.UserID.Valid {
			accTransfer.IdentificationID = extAccIdent.ID
		} else {
			accTransfer.IdentificationID = emailIdent.ID
			accTransfer.ToLinkIdentificationID = null.StringFrom(extAccIdent.ID)
		}

		err = o.accountTransfersRepo.Insert(ctx, tx, &accTransfer)
		if err != nil {
			return nil, nil, err
		}

		// add transfer obj to verification
		ver.AccountTransferID = null.StringFrom(accTransfer.ID)
		if err = o.verificationRepo.UpdateAccountTransferID(ctx, tx, ver); err != nil {
			return nil, nil, err
		}

		return nil, &accTransfer, apierror.IdentificationExists(extAccIdent.Type, nil)
	} else if extAccIdent != nil { //
		// dangling external_account, that hasn't been resolved. attach it to this sign-up.
		existingExternalAccount, err := o.externalAccountService.Update(ctx, tx, userSettings, ost, oauthUser, env.Instance)
		if err != nil {
			return nil, nil, err
		}
		externalAccount = existingExternalAccount
	} else {
		// Create an external account, along with an oauth identification
		creationResult, err := o.externalAccountService.Create(ctx, tx, ver, ost, oauthUser, env.Instance.ID, nil)
		if err != nil {
			return nil, nil, err
		}
		externalAccount = creationResult.ExternalAccount

		// Check if a verified email address identification with the same
		// identifier as the oauth provider exists. If yes, link the
		// external account identification with it. Otherwise, see if we
		// need to create a new identification.
		existingIdent, err := o.findVerifiedEmailIdentificationWithIdentifier(ctx, tx, signUp.EmailAddressID, oauthUser.EmailAddress)
		if err != nil {
			return nil, nil, err
		}
		if existingIdent != nil {
			extAccIdent, err = o.externalAccountService.LinkIdentification(ctx, tx, creationResult.Identification, existingIdent, oauthUser)
			if err != nil {
				return nil, nil, err
			}
		} else {
			extAccIdent, err = o.externalAccountService.CreateOrLinkEmailIdentification(ctx, tx, creationResult.Identification, ost, oauthUser, env.Instance.ID, nil, userSettings)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	signUp.SuccessfulExternalAccountIdentificationID = null.StringFrom(extAccIdent.ID)
	err = o.signUpRepo.UpdateSuccessfulExternalAccountIdentificationID(ctx, tx, signUp)
	if err != nil {
		return nil, nil, err
	}

	rotatingTokenNonce, err := generateRotatingTokenNonce(ost)
	if err != nil {
		return nil, nil, fmt.Errorf("finishOauthForSignUp: generating rotating_token_nonce for client %s: %w", client.ID, err)
	}

	newSession, err := o.signUpService.FinalizeFlow(
		ctx,
		tx,
		sign_up.FinalizeFlowParams{
			SignUp:             signUp,
			Env:                env,
			Client:             client,
			ExternalAccount:    externalAccount,
			UserSettings:       usersettings.NewUserSettings(env.AuthConfig.UserSettings),
			RotatingTokenNonce: rotatingTokenNonce,
		},
	)
	return newSession, nil, err
}

// findExistingUserIDForSignUp locates a user ID by their external account or email identifications, depending on
// whether the OAuth email is verified or not and the ClerkJS version supports the email verification flow.
func (o *OAuth) findExistingUserIDForSignUp(ctx context.Context, oauthUser *oauth.User, extAccIdent *model.Identification, emailIdent *model.Identification) (bool, *string) {
	var existingUserID *string
	if oauthUser.EmailAddressVerified || unverifiedemails.IsVerifyFlowSupportedByClerkJSVersion(ctx) {
		existingUserID = identifications.FindUserIDIfExists(extAccIdent, emailIdent)
	} else {
		existingUserID = identifications.FindUserIDIfExists(extAccIdent)
	}
	return existingUserID != nil, existingUserID
}

func (o OAuth) finishConnectOauthAccount(
	ctx context.Context,
	tx database.Tx,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	ver *model.Verification,
	ost *model.OauthStateToken,
	oauthUser *oauth.User) error {
	if apiErr := o.ensureUserSignedIn(ctx, instance.ID, ost.SourceID); apiErr != nil {
		return apiErr
	}

	existingSameProviderAccount, err := o.externalAccountRepo.QueryVerifiedByUserIDAndProviderAndInstance(ctx, o.deps.DB(), ost.SourceID, ver.Strategy, instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if existingSameProviderAccount != nil {
		return apierror.OAuthAccountAlreadyConnected(ver.Strategy)
	}

	// Check if email is already used by some other user of the instance, and it is verified
	identification, err := o.identificationRepo.QueryClaimedByInstanceAndIdentifier(ctx, o.deps.DB(), instance.ID, oauthUser.EmailAddress)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if identification != nil && !identification.IsClaimableBy(ost.SourceID) {
		return apierror.OAuthIdentificationClaimed()
	}

	// Check if email is restricted for the instance
	blockOAuthEmailSubaddresses := cenv.IsEnabled(cenv.FlagOAuthBlockEmailSubaddresses) && userSettings.Social[ver.Strategy].BlockEmailSubaddresses
	restrictionSettings := restrictions.Settings{
		Restrictions: usersettingsmodel.Restrictions{
			BlockEmailSubaddresses: usersettingsmodel.BlockEmailSubaddresses{
				Enabled: blockOAuthEmailSubaddresses || userSettings.Restrictions.BlockEmailSubaddresses.Enabled,
			},
		},
	}
	res, err := o.restrictionService.Check(
		ctx,
		o.deps.DB(),
		restrictions.Identification{
			Identifier:          oauthUser.EmailAddress,
			CanonicalIdentifier: emailaddress.Canonical(oauthUser.EmailAddress),
			Type:                constants.ITEmailAddress,
		},
		restrictionSettings,
		instance.ID,
	)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if res.Blocked || !res.Allowed {
		return apierror.IdentifierNotAllowedAccess(oauthUser.EmailAddress)
	}

	err = o.externalAccountService.Connect(ctx, tx, ver, ost, oauthUser, instance.ID, ost.SourceID, userSettings)
	if err != nil {
		return err
	}

	user, err := o.userRepo.FindByID(ctx, tx, ost.SourceID)
	if err != nil {
		return err
	}
	if !user.ProfileImagePublicURL.Valid && oauthUser.AvatarURL != "" {
		user.ProfileImagePublicURL = null.StringFrom(oauthUser.AvatarURL)
		err := o.userRepo.UpdateProfileImage(ctx, tx, user)
		if err != nil {
			return err
		}
	}
	if err := o.sendUserUpdatedEvent(ctx, tx, instance, userSettings, user); err != nil {
		return fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", user, instance.ID, err)
	}

	return nil
}

func (o OAuth) finishOAuthForReauthorize(
	ctx context.Context,
	tx database.Tx,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	ost *model.OauthStateToken,
	oauthUser *oauth.User,
) error {
	user, err := o.userRepo.QueryByInstanceAndExternalAccountID(ctx, tx, instance.ID, ost.SourceID)
	if err != nil {
		return err
	}
	if user == nil {
		return apierror.ResourceNotFound()
	}

	if apiErr := o.ensureUserSignedIn(ctx, instance.ID, user.ID); apiErr != nil {
		return apiErr
	}

	return o.externalAccountService.Reauthorize(ctx, tx, userSettings, instance, user, ost, oauthUser)
}

func (o OAuth) isEmailAddressAllowed(
	ctx context.Context,
	exec database.Executor,
	emailAddress string,
	userSettings *usersettings.UserSettings,
	strategy string,
	instanceID string,
	testMode bool,
) apierror.Error {
	restrictionSettings := userSettings.Restrictions
	if cenv.IsEnabled(cenv.FlagOAuthBlockEmailSubaddresses) && userSettings.Social[strategy].BlockEmailSubaddresses {
		restrictionSettings.BlockEmailSubaddresses.Enabled = true
	}
	res, err := o.restrictionService.Check(
		ctx,
		exec,
		restrictions.Identification{
			Identifier:          emailAddress,
			Type:                constants.ITEmailAddress,
			CanonicalIdentifier: emailaddress.Canonical(emailAddress),
		},
		restrictions.Settings{
			Restrictions: restrictionSettings,
			TestMode:     testMode,
		},
		instanceID,
	)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if res.Blocked || !res.Allowed {
		return apierror.IdentifierNotAllowedAccess(res.Identifier)
	}
	return nil
}

func (o OAuth) ensureUserSignedIn(ctx context.Context, instanceID, userID string) apierror.Error {
	activeUserSessions, err := o.clientDataService.FindAllUserSessions(ctx, instanceID, userID, client_data.SessionFilterActiveOnly())
	if err != nil {
		return apierror.Unexpected(err)
	}

	if len(activeUserSessions) == 0 {
		return apierror.SignedOut()
	}

	return nil
}

// handle development instances redirect response:
//   - for development, requests come in from clerk.shared.lcl.dev/v1/oauth_callback
//   - so, we need to redirect to the instance's FAPI and put a cookie there
//     before redirecting to the final location.
func respondWithNewClientForDevelopment(
	w http.ResponseWriter,
	r *http.Request,
	domain *model.Domain,
	client *model.Client,
	csrfToken, finalRedirectURL string) {
	// Redirect with 307 to the instance's FAPI (e.g.
	// clerk.happy.hippo-1.lcl.dev or happy-hippo-1.clerk.accounts.dev,
	// depending on whether we're using publishable key or not)
	redirectURL := fmt.Sprintf("%s/v1/oauth_callback?_set_cookie_client_id=%s&_set_cookie_subdomain=clerk&%s=%s&_csrf_token=%s&_final_redirect_url=%s",
		domain.FapiURL(),
		client.ID,
		param.RetObjType,
		constants.RORedirect,
		csrfToken,
		url.QueryEscape(finalRedirectURL),
	)

	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}

func redirectWithNewClientForProduction(
	w http.ResponseWriter,
	r *http.Request,
	db database.Database,
	cache cache.Cache,
	dmn *model.Domain,
	client *model.Client,
	finalRedirectURL string) {
	// drop a cookie on clerk.example.com
	_ = cookies.SetClientCookie(r.Context(), db, cache, w, client, dmn.AuthHost())

	http.Redirect(w, r, finalRedirectURL, http.StatusTemporaryRedirect)
}

// Based on the state parameter, which contains the nonce, attempt to find the corresponding verification
func (o *OAuth) verificationFromStateParam(ctx context.Context, r *http.Request) (*model.Verification, error) {
	state := r.FormValue("state")
	if state == "" {
		return nil, clerkerrors.WithStacktrace("oauth: missing state parameter")
	}

	verification, err := o.verificationRepo.QueryByNonce(ctx, o.deps.DB(), state)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("oauth: invalid state: %w", err)
	}
	if verification == nil {
		return nil, clerkerrors.WithStacktrace("oauth: verification not found")
	}

	if !verification.Token.Valid {
		return nil, clerkerrors.WithStacktrace("oauth: token does not exist for nonce: %w", err)
	}

	return verification, nil
}

var (
	ErrMismatchedClientID = errors.New("mismatched client id")
)

// Parses and verifies the OAuth state token of a verification using the public key of the provided instance.
func (o *OAuth) oauthStateTokenFromVerification(ctx context.Context, ver *model.Verification, instanceID string) (*model.OauthStateToken, error) {
	instance, err := o.instanceRepo.FindByID(ctx, o.deps.DB(), instanceID)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("oauth: invalid instanceID: %w", err)
	}

	pubKey, err := pkiutils.LoadPublicKey([]byte(instance.PublicKey))
	if err != nil {
		return nil, clerkerrors.WithStacktrace("oauth: unable to parse instance public key: %w", err)
	}

	verifiedClaims := model.OauthStateTokenClaims{}
	err = jwt.Verify(ver.Token.String, pubKey, &verifiedClaims, o.deps.Clock(), instance.KeyAlgorithm)
	if err != nil {
		return nil, clerkerrors.WithStacktrace("oauth: %w", err)
	}

	// For Native flows (e.g. mobile) we ignore the client check as it's not necessary to start and complete
	// an OAuth flow from the same client (can use different browsers)
	if verifiedClaims.ClientType.IsNative() {
		return verifiedClaims.ToOauthStateToken(), nil
	}

	client, ok := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	if !ok || client == nil {
		if instance.IsProduction() {
			return nil, clerkerrors.WithStacktrace("oauth: no client present")
		}
		return verifiedClaims.ToOauthStateToken(), nil
	}

	if client.ID != verifiedClaims.ClientID {
		return nil, clerkerrors.WithStacktrace("oauth: mismatched client ID, expected %s but got %s: %w",
			client.ID, verifiedClaims.ClientID, ErrMismatchedClientID)
	}

	return verifiedClaims.ToOauthStateToken(), nil
}

func (o *OAuth) fetchOAuth1AccessToken(
	ctx context.Context, exec database.Executor,
	ost *model.OauthStateToken,
	provider oauth.Provider,
	instanceID,
	consumerKey, consumerSecret,
	oauthToken, oauthVerifier string,
) (*oauth1.AccessToken, error) {
	// the spec mandates, for security purposes, to ensure that the
	// provided `oauth_token` parameter is the same with the one we
	// received in the pre-callback step (i.e. fetch request access token)
	requestToken, err := o.oauth1RequestTokensRepo.FindByIDAndToken(
		ctx, exec, ost.Nonce, oauthToken, instanceID)
	if err != nil {
		return nil, err
	}

	// we no longer need the request token
	_ = o.oauth1RequestTokensRepo.DeleteByID(ctx, exec, requestToken.ID)

	consumer := oauth1.NewConsumer(
		consumerKey,
		consumerSecret,
		oauth1.ServiceProvider{
			RequestTokenUrl:   provider.RequestTokenURL(),
			AuthorizeTokenUrl: provider.AuthURL(),
			AccessTokenUrl:    provider.TokenURL(),
		},
	)

	accessToken, err := consumer.AuthorizeToken(
		&oauth1.RequestToken{
			Token:  requestToken.Token,
			Secret: requestToken.Secret,
		}, oauthVerifier)
	if err != nil {
		return nil, err
	}

	return accessToken, nil
}

func (o *OAuth) isRedirectURLAllowed(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	redirectURL string,
) (bool, error) {
	if instance.IsDevelopmentOrStaging() {
		return true, nil
	}

	exists, err := o.redirectUrlsRepo.ExistsByURLAndInstance(ctx, exec, redirectURL, instance.ID)
	if err != nil {
		return false, err
	}

	return exists, nil
}

// returns true if p is an OAuth 2.0 provider; false if it's an OAuth 1.0
// provider
func isOAuth2(p oauth.Provider) bool {
	return !p.IsOAuth1()
}

func generateRotatingTokenNonce(ost *model.OauthStateToken) (*string, error) {
	if !ost.ClientType.IsNative() {
		return nil, nil
	}

	t, err := rand.Token()
	if err != nil {
		return nil, err
	}

	return &t, nil
}

func (o *OAuth) findVerifiedEmailIdentificationWithIdentifier(
	ctx context.Context,
	exec database.Executor,
	identificationID null.String,
	identifier string,
) (*model.Identification, error) {
	if !identificationID.Valid {
		return nil, nil
	}
	if identifier == "" {
		return nil, nil
	}

	existingIdent, err := o.identificationRepo.QueryEmailByID(ctx, exec, identificationID.String)
	if err != nil {
		return nil, err
	}
	if existingIdent == nil || !existingIdent.IsVerified() || existingIdent.Identifier != null.StringFrom(identifier) {
		return nil, nil
	}
	return existingIdent, nil
}

func (o OAuth) getOAuthConfigFromOAuthStateToken(ctx context.Context, ost *model.OauthStateToken, instanceID string) (*model.OauthConfig, error) {
	// TODO(oauth): this is a smell, because it's a place where the logic is
	// duplicated (see EnabledSSOProvider.UsesDevConfig()). We should
	// find a way to remove that duplication and have the logic live in a
	// single place.
	if ost.OauthConfigID == "" {
		return model.DevOauthConfig(ost.Provider)
	}

	return o.oauthConfigRepo.FindByIDAndInstance(ctx, o.deps.DB(), ost.OauthConfigID, instanceID)
}

func (o OAuth) sendUserUpdatedEvent(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User) error {
	userSerializable, err := o.serializableService.ConvertUser(ctx, exec, userSettings, user)
	if err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: serializing user %+v: %w", user, err)
	}

	if err = o.eventService.UserUpdated(ctx, exec, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: send user updated event for user %s: %w", user.ID, err)
	}
	return nil
}

func (o OAuth) updateClientForAccountTransfer(ctx context.Context, client *model.Client, toSignUpAccountTransfer, toSignInAccountTransfer *model.AccountTransfer) error {
	if toSignInAccountTransfer != nil {
		client.ToSignInAccountTransferID = null.StringFrom(toSignInAccountTransfer.ID)
		cdsClient := client_data.NewClientFromClientModel(client)
		if err := o.clientDataService.UpdateClientToSignInAccountTransferID(ctx, cdsClient); err != nil {
			return err
		}
		cdsClient.CopyToClientModel(client)
	}
	if toSignUpAccountTransfer != nil {
		client.ToSignUpAccountTransferID = null.StringFrom(toSignUpAccountTransfer.ID)
		cdsClient := client_data.NewClientFromClientModel(client)
		if err := o.clientDataService.UpdateClientToSignUpAccountTransferID(ctx, cdsClient); err != nil {
			return err
		}
		cdsClient.CopyToClientModel(client)
	}
	return nil
}

// enforceSingleSessionMode ensures that there is no active session in the requesting Client for the User,
// when single-session mode is activated in the instance.
func (o OAuth) checkSingleSessionMode(ctx context.Context, env *model.Env, client *model.Client, userID string) (*model.Session, error) {
	if !env.AuthConfig.SessionSettings.SingleSessionMode {
		return nil, nil
	}
	return o.clientActiveUserSession(ctx, env, client, userID)
}

func (o OAuth) clientActiveUserSession(ctx context.Context, env *model.Env, client *model.Client, userID string) (*model.Session, error) {
	clientActiveSessions, err := o.clientDataService.FindAllClientSessions(ctx, env.Instance.ID, client.ID, client_data.SessionFilterActiveOnly())
	if err != nil {
		return nil, err
	}
	if len(clientActiveSessions) == 0 {
		return nil, nil
	}
	var userSession *model.Session
	for _, sess := range clientActiveSessions {
		if sess.UserID == userID {
			userSession = sess.ToSessionModel()
			break
		}
	}
	return userSession, nil
}

type redirectURLOptions struct {
	checkSession bool
	session      *model.Session
	samlURL      *string
}

func determineRedirectURL(ost *model.OauthStateToken, opts redirectURLOptions) string {
	if opts.checkSession && ost.ActionCompleteRedirectURL.Valid && opts.session != nil {
		return ost.ActionCompleteRedirectURL.String
	} else if opts.samlURL != nil {
		// Redirect to the SAML IdP
		return *opts.samlURL
	}
	return ost.RedirectURL
}
