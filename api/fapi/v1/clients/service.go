package clients

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"clerk/api/apierror"
	fapicookies "clerk/api/fapi/v1/cookies"
	"clerk/api/fapi/v1/tokens"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/clients"
	sharedcookies "clerk/api/shared/cookies"
	"clerk/api/shared/events"
	"clerk/api/shared/session_activities"
	"clerk/api/shared/sign_in"
	"clerk/api/shared/sign_up"
	"clerk/api/shared/token"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/auth"
	"clerk/pkg/cache"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/cookies"
	"clerk/pkg/ctx/activity"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/jwtcontext"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/request_info"
	"clerk/pkg/ctx/requesting_session"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/jwt"
	clerkmaintenance "clerk/pkg/maintenance"
	"clerk/pkg/psl"
	"clerk/pkg/rand"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/versions"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"
	pkiutils "clerk/utils/pki"
	urlUtils "clerk/utils/url"

	josejwt "github.com/go-jose/go-jose/v3/jwt"
	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/segmentio/ksuid"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	cache cache.Cache
	clock clockwork.Clock
	db    database.Database

	// services
	clientService            *clients.Service
	sharedCookieService      *sharedcookies.Service
	fapiCookieService        *fapicookies.Service
	eventService             *events.Service
	signInService            *sign_in.Service
	signUpService            *sign_up.Service
	tokenService             *token.Service
	tokensService            *tokens.Service
	sessionActivitiesService *session_activities.Service

	// repositories
	domainRepo        *repository.Domain
	signInRepo        *repository.SignIn
	signUpRepo        *repository.SignUp
	syncNonceRepo     *repository.SyncNonces
	clientDataService *client_data.Service
}

func NewService(deps clerk.Deps) *Service {
	clock := deps.Clock()

	return &Service{
		cache:                    deps.Cache(),
		clock:                    clock,
		db:                       deps.DB(),
		clientService:            clients.NewService(deps),
		sharedCookieService:      sharedcookies.NewService(deps),
		fapiCookieService:        fapicookies.NewService(deps),
		eventService:             events.NewService(deps),
		signInService:            sign_in.NewService(deps),
		signUpService:            sign_up.NewService(deps),
		tokenService:             token.NewService(),
		tokensService:            tokens.NewService(deps),
		sessionActivitiesService: session_activities.NewService(),
		domainRepo:               repository.NewDomain(),
		signInRepo:               repository.NewSignIn(),
		signUpRepo:               repository.NewSignUp(),
		syncNonceRepo:            repository.NewSyncNonces(),
		clientDataService:        client_data.NewService(deps),
	}
}

// SetRequestingClient sets the requesting client into the request's context
// The client can be retrieved via:
// A. The __client JWT ID and rotating token.
// In this scenario when there's no DevBrowser, we just need to make sure client ID and Rotating Token match
// B. The rotating token nonce
// Everytime a client session changes the Rotating Token is updated. As a result any subsequent request from
// a client will return 401 to prevent session fixation attacks. However there are some scenarios such as the
// OAuth flow in native applications where the original clients can use a nonce to load the client once regardless
// of the different Rotating Token.
func (s *Service) SetRequestingClient(ctx context.Context, rotatingTokenNonce string) (context.Context, apierror.Error) {
	env := environment.FromContext(ctx)
	var clientID string
	var rotatingToken string

	devBrowser := requestingdevbrowser.FromContext(ctx)
	hasDevBrowser := devBrowser != nil

	if hasDevBrowser && devBrowser.ClientID.Valid {
		clientID = devBrowser.ClientID.String
	} else {
		jwt := jwtcontext.FromContext(ctx)
		clientID, _ = jwt["id"].(string)
		rotatingToken, _ = jwt["rotating_token"].(string)
	}
	log.AddToLogLine(ctx, log.ClientID, clientID)
	log.AddToLogLine(ctx, log.RotatingToken, rotatingToken)

	// Ignore client DB errors on purpose and treat them as a 401 response
	client, _ := s.getRequestingClient(ctx, clientID, rotatingToken, rotatingTokenNonce, env.Instance.ID)
	return context.WithValue(ctx, ctxkeys.RequestingClient, client), nil
}

// Return a client. Try to find the client by clientID and rotatingToken first.
// If that fails, fall back to the currently active client for the instance.
func (s *Service) getRequestingClient(
	ctx context.Context,
	clientID string,
	rotatingToken string,
	rotatingTokenNonce string,
	instanceID string,
) (*model.Client, error) {
	if clientID == "" {
		return nil, nil
	}

	// See if we should fetch the client by ID and rotating token.
	if rotatingToken != "" {
		var client *model.Client
		if rotatingTokenNonce == "" {
			cdsClient, err := s.clientDataService.FindClientByIDAndRotatingToken(ctx, instanceID, clientID, rotatingToken)
			if err != nil {
				return nil, err
			}

			client = cdsClient.ToClientModel()
		} else {
			cdsClient, err := s.clientDataService.FindClientByIDAndRotatingTokenNonce(ctx, instanceID, clientID, rotatingTokenNonce)
			if err != nil {
				return nil, err
			}

			cdsClient.RotatingTokenNonce = null.StringFromPtr(nil)
			if err := s.clientDataService.UpdateClientRotatingTokenNonce(ctx, instanceID, cdsClient); err != nil {
				return nil, err
			}

			client = cdsClient.ToClientModel()
		}
		return client, nil
	}

	// If there's no rotating token, find the currently active client for the instance.
	client, err := s.clientDataService.FindClient(ctx, instanceID, clientID)
	if err != nil {
		if errors.Is(err, client_data.ErrNoRecords) {
			return nil, nil
		}
		return nil, err
	}
	return client.ToClientModel(), nil
}

// GetClientFromContext returns the client stored into the context
func (s *Service) GetClientFromContext(ctx context.Context) (*model.Client, apierror.Error) {
	// TODO: I needed to move the RequestingClient determination earlier
	// with the setRequestingClient middleware.
	// Other places rely on this function to re-set the RequestingClient
	// on the context. That should be cleaned up.

	client, _ := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	if client == nil {
		return nil, apierror.ClientNotFoundInRequest()
	}
	return client, nil
}

type UpdateCookieResponse struct {
	SourceType string
	SourceID   string
	Body       interface{}
	Client     *model.Client
}

// UpdateClientCookieIfNeeded checks whether the `postponeCookieUpdate` is set in the client.
// If it is set, it will reset it to `false` and update the client's cookie accordingly.
func (s *Service) UpdateClientCookieIfNeeded(ctx context.Context) (*UpdateCookieResponse, apierror.Error) {
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !client.PostponeCookieUpdate {
		return nil, nil
	}

	var updateCookie UpdateCookieResponse
	updateCookie.Client = client

	var newSessionID null.String
	if client.SignInID.Valid {
		// we have a sign in
		updateCookie.SourceID = client.SignInID.String
		updateCookie.SourceType = constants.ROSignIn

		var signIn *model.SignIn
		txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
			si, err := s.signInRepo.FindByID(ctx, tx, client.SignInID.String)
			if err != nil {
				return true, err
			}
			signIn = si

			updateCookie.Body, err = s.toSignInResponse(ctx, tx, signIn, userSettings)
			if err != nil {
				return true, err
			}
			return false, nil
		})
		if txErr != nil {
			return nil, apierror.Unexpected(txErr)
		}

		newSessionID = signIn.CreatedSessionID
	} else if client.SignUpID.Valid {
		// we have a sign up
		updateCookie.SourceID = client.SignUpID.String
		updateCookie.SourceType = constants.ROSignUp

		var signUp *model.SignUp
		txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
			su, err := s.signUpRepo.FindByID(ctx, tx, client.SignUpID.String)
			if err != nil {
				return true, err
			}
			signUp = su

			updateCookie.Body, err = s.toSignUpResponse(ctx, tx, signUp, userSettings)
			if err != nil {
				return true, err
			}
			return false, nil
		})
		if txErr != nil {
			return nil, apierror.Unexpected(txErr)
		}

		newSessionID = signUp.CreatedSessionID
	}

	// Up to this point the operation can be repeated. The following call will rotate the rotating token
	// so the client cookie will no longer match the DB state.
	if err := s.sharedCookieService.UpdateClientCookieValue(ctx, env.Instance, client); err != nil {
		return nil, apierror.Unexpected(
			fmt.Errorf("updateClientCookieIfNeeded: updating client cookie for %s: %w", client.ID, err))
	}

	if err := s.resetPostponedCookieUpdateForClient(ctx, env.Instance, client); err != nil {
		return nil, apierror.Unexpected(err)
	}

	// If we're in this flow, it most likely means that a new session was created from a sign in or sign up,
	// but the device represented by the "SessionActivity" should be set for the device where the cookie gets set,
	// not the device that "finalized" the flow.
	if newSessionID.Valid {
		if apiErr := s.ensureActiveSessionWithActivity(ctx, env.Instance, client, newSessionID.String); apiErr != nil {
			return nil, apiErr
		}
	} else {
		return nil, apierror.InvalidAuthentication()
	}

	return &updateCookie, nil
}

func (s *Service) toSignInResponse(
	ctx context.Context,
	exec database.Executor,
	signIn *model.SignIn,
	userSettings *usersettings.UserSettings) (*serialize.SignInResponse, error) {
	signInSerializable, err := s.signInService.ConvertToSerializable(ctx, exec, signIn, userSettings, "")
	if err != nil {
		return nil, fmt.Errorf("toSignInResponse: converting %+v to serializable: %w",
			signIn, err)
	}

	response, err := serialize.SignIn(s.clock, signInSerializable, userSettings)
	if err != nil {
		return nil, fmt.Errorf("toSignInResponse: serializing sign in %+v: %w",
			signInSerializable, err)
	}

	return response, nil
}

func (s *Service) toSignUpResponse(
	ctx context.Context,
	exec database.Executor,
	signUp *model.SignUp,
	userSettings *usersettings.UserSettings,
) (*serialize.SignUpResponse, error) {
	signUpSerializable, err := s.signUpService.ConvertToSerializable(ctx, exec, signUp, userSettings, "")
	if err != nil {
		return nil, fmt.Errorf("toSignUpResponse: converting %+v to serializable: %w",
			signUp, err)
	}

	response, err := serialize.SignUp(ctx, s.clock, signUpSerializable)
	if err != nil {
		return nil, fmt.Errorf("toSignUpResponse: serializing sign up %+v: %w",
			signUpSerializable, err)
	}

	return response, nil
}

func (s *Service) resetPostponedCookieUpdateForClient(ctx context.Context, instance *model.Instance, client *model.Client) error {
	client.PostponeCookieUpdate = false
	cdsClient := client_data.NewClientFromClientModel(client)
	if client.SignInID.Valid {
		cdsClient.SignInID = null.StringFromPtr(nil)
		if err := s.clientDataService.UpdateClientPostponeCookieUpdateAndSignInID(ctx, instance.ID, cdsClient); err != nil {
			return fmt.Errorf("updateClientCookieIfNeeded: updating sign_in_id of %s: %w", client.ID, err)
		}
	} else if client.SignUpID.Valid {
		cdsClient.SignUpID = null.StringFromPtr(nil)
		if err := s.clientDataService.UpdateClientPostponeCookieUpdateAndSignUpID(ctx, instance.ID, cdsClient); err != nil {
			return apierror.Unexpected(fmt.Errorf("updateClientCookieIfNeeded: updating sign_up_id of %s: %w", client.ID, err))
		}
	} else {
		if err := s.clientDataService.UpdateClientPostponeCookieUpdate(ctx, instance.ID, cdsClient); err != nil {
			return apierror.Unexpected(fmt.Errorf("updateClientCookieIfNeeded: updating sign_up_id of %s: %w", client.ID, err))
		}
	}
	// Required to mutate fields on the model.Client object
	cdsClient.CopyToClientModel(client)

	return nil
}

func (s *Service) ensureActiveSessionWithActivity(ctx context.Context, instance *model.Instance, client *model.Client, sessionID string) apierror.Error {
	sess, err := s.clientDataService.FindSession(ctx, instance.ID, client.ID, sessionID)
	if err != nil {
		if errors.Is(err, client_data.ErrNoRecords) {
			return apierror.SessionNotFound(sessionID)
		}
		return apierror.Unexpected(err)
	}
	if !sess.ToSessionModel().IsActive(s.clock) {
		return apierror.InvalidAuthorization()
	}
	deviceActivity := activity.FromContext(ctx)
	if deviceActivity != nil {
		deviceActivity.SessionID = null.StringFrom(sess.ID)
		if err := s.sessionActivitiesService.CreateSessionActivity(ctx, s.db, instance.ID, deviceActivity); err != nil {
			return apierror.Unexpected(fmt.Errorf("sessions/create: inserting new session activity %+v: %w", deviceActivity, err))
		}

		sess.SessionActivityID = null.StringFrom(deviceActivity.ID)
		if err := s.clientDataService.UpdateSessionSessionActivityID(ctx, sess); err != nil {
			return apierror.Unexpected(fmt.Errorf("sessions/create: updating session activity id on %s: %w", sess.ID, err))
		}
	}
	return nil
}

// Create creates a new client along with its cookie
func (s *Service) Create(ctx context.Context) (*model.Client, apierror.Error) {
	env := environment.FromContext(ctx)
	return s.CreateClient(ctx, env.Instance)
}

// Retrieves the current client that is loaded into the given context
func (s *Service) Read(ctx context.Context) (*serialize.ClientResponseClientAPI, apierror.Error) {
	client, apierr := s.GetClientFromContext(ctx)
	if apierr != nil {
		return nil, nil
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	clientWithSessions, apierr := s.clientService.ConvertToClientWithSessions(ctx, client, env)
	if apierr != nil {
		return nil, apierr
	}

	requestInfo := request_info.FromContext(ctx)

	// PERF: generate valid tokens for each session as a performance
	// optimization, in order to avoid an initial token generation
	// request that Clerk.js would otherwise had to make
	iss, err := s.tokenService.GetIssuer(ctx, s.db, env.Domain, env.Instance)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	for i, sessionWithUser := range clientWithSessions.CurrentSessions {
		clientWithSessions.CurrentSessions[i].Token, err = token.GenerateSessionToken(
			ctx,
			s.clock,
			s.db,
			env,
			sessionWithUser.Session,
			requestInfo.Origin,
			iss,
		)
		if err != nil && !errors.Is(err, auth.ErrInactiveSession) && !errors.Is(err, token.ErrUserNotFound) {
			return nil, apierror.Unexpected(err)
		}
	}

	response, err := serialize.ClientToClientAPI(ctx, s.clock, clientWithSessions, userSettings)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return response, nil
}

// Delete ends all sessions of the current client
func (s *Service) Delete(ctx context.Context) (*serialize.ClientResponseClientAPI, apierror.Error) {
	client, _ := s.GetClientFromContext(ctx)
	if client == nil {
		return nil, nil
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if err := s.clientService.EndAllSessions(ctx, env.Instance, client); err != nil {
		return nil, apierror.Unexpected(err)
	}

	if err := s.sharedCookieService.UpdateClientCookieValue(ctx, env.Instance, client); err != nil {
		return nil, apierror.Unexpected(
			fmt.Errorf("sessions/delete: updating client cookie for %s: %w", client.ID, err),
		)
	}

	clientWithSessions, apiErr := s.clientService.ConvertToClientWithSessions(ctx, client, env)
	if apiErr != nil {
		return nil, apiErr
	}

	response, err := serialize.ClientToClientAPI(ctx, s.clock, clientWithSessions, userSettings)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return response, nil
}

// CreateClient creates and returns a new client
func (s *Service) CreateClient(ctx context.Context, instance *model.Instance) (*model.Client, apierror.Error) {
	var devBrowserID *string

	if devBrowser := requestingdevbrowser.FromContext(ctx); devBrowser != nil {
		devBrowserID = &devBrowser.ID
	}

	client, err := s.CreateWithDevBrowser(ctx, instance, devBrowserID)

	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return client, nil
}

func (s *Service) CreateWithDevBrowser(ctx context.Context, instance *model.Instance, devBrowserID *string) (*model.Client, error) {
	rotatingToken, err := rand.Token()
	if err != nil {
		return nil, err
	}

	client := client_data.NewClientFromClientModel(&model.Client{Client: &sqbmodel.Client{
		InstanceID:    instance.ID,
		RotatingToken: rotatingToken,
	}})

	if err := s.clientDataService.CreateClient(ctx, instance.ID, client); err != nil {
		return nil, err
	}
	newCookieVal, err := model.ClientCookieClass.CreateClientCookieValue(instance.PrivateKey, instance.KeyAlgorithm, client.ID, client.RotatingToken, devBrowserID)
	if err != nil {
		return nil, err
	}
	client.CookieValue = null.StringFrom(newCookieVal)
	if err := s.clientDataService.UpdateClientCookieValue(ctx, instance.ID, client); err != nil {
		return nil, err
	}

	return client.ToClientModel(), nil
}

type syncParams struct {
	RedirectURL string `validate:"required"`
	LinkDomain  string
}

func (p syncParams) validate(requiresLinkDomain bool) apierror.Error {
	validator := validator.New()
	var errs apierror.Error
	if err := validator.Struct(p); err != nil {
		errs = apierror.Combine(errs, apierror.FormValidationFailed(err))
	}
	if requiresLinkDomain && p.LinkDomain == "" {
		errs = apierror.Combine(errs, apierror.FormMissingParameter(paramLinkDomain))
	}
	if errs != nil {
		return errs
	}
	return nil
}

// Sync returns the URL to navigate to in order to link a client between a
// satellite and a primary domain. The URL will include a token that uniquely
// identifies the client in the query parameters.
// If there's no client, Sync returns the params.RedirectURL value as is.
// Sync can be accessed from both primary and satellite domains. Linking can
// only be done if Sync is called from a primary domain. If Sync is accessed
// from a satellite domain it will redirect to the instance's primary domain
// first, in order to sync from there.
func (s *Service) Sync(ctx context.Context, params syncParams) (*url.URL, apierror.Error) {
	env := environment.FromContext(ctx)
	var client *model.Client
	var err error
	var location *url.URL
	isPrimary := !env.Domain.IsSatellite(env.Instance)

	if err := params.validate(isPrimary); err != nil {
		return nil, err
	}

	// If we're on a satellite domain, redirect to the primary so we can
	// sync with that. Pass the satellite domain as a query parameter so
	// that main knows where the request came from.
	if !isPrimary {
		primaryDomain, err := s.domainRepo.FindByID(ctx, s.db, env.Instance.ActiveDomainID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		location, err := buildSyncURL(primaryDomain.FapiURL(), syncParams{
			RedirectURL: params.RedirectURL,
			LinkDomain:  env.Domain.Name,
		})
		if err != nil {
			return nil, apierror.FormInvalidParameterValue(paramLinkDomain, params.LinkDomain)
		}
		return location, nil
	}

	// We're now on the primary domain. Get the satellite domain that we'll link to.
	linkDomain, err := s.domainRepo.QueryByNameAndInstance(ctx, s.db, params.LinkDomain, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if linkDomain == nil {
		return nil, apierror.ResourceForbidden()
	}

	// Fetch the client from the context. A client will be available for
	// production environments, or for development environments without URL-based session syncing
	// with first party hosts.
	client, _ = ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	// If we still don't have a client, just return the redirect URL from the
	// params.
	if client == nil {
		location, err = url.Parse(params.RedirectURL)
		if err != nil {
			return nil, apierror.FormInvalidParameterValue(paramRedirectURL, params.RedirectURL)
		}
		return withSyncedQuery(location), nil
	}

	// We have a client. Create a token that can be used for syncing.
	syncNonce, err := s.createSyncNonce(ctx, client)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Generate the link token.
	claims := linkTokenClaims{
		SyncNonce:   syncNonce,
		RedirectURL: params.RedirectURL,
	}
	token, err := s.generateLinkToken(claims, env.Instance.PrivateKey, env.Instance.KeyAlgorithm)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Build the URL to link the client to the satellite domain.
	location, err = buildLinkURL(linkDomain.FapiURL(), token)
	if err != nil {
		return nil, apierror.FormInvalidParameterValue(paramLinkDomain, params.LinkDomain)
	}
	return location, nil
}

type PrimarySyncParams struct {
	RedirectURL     string `validate:"required"`
	SatelliteDomain string
}

func (s *Service) PrimarySync(ctx context.Context, params PrimarySyncParams) (*url.URL, apierror.Error) {
	env := environment.FromContext(ctx)
	var client *model.Client
	var location *url.URL

	// We're now on the primary domain. Get the satellite domain that we are syncing with.
	satelliteDomain, err := s.domainRepo.QueryByNameAndInstance(ctx, s.db, params.SatelliteDomain, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if satelliteDomain == nil {
		return nil, apierror.ResourceForbidden()
	}

	// Fetch the client from the context. A client will be available for
	// production environments, or for development environments without URL-based session syncing
	// with first party hosts.
	client, _ = ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	// If we don't have a client, return an empty syncNonce. This will be passed to the satellite FAPI, ensuring any stale
	// satellite session cookies are cleaned up.
	syncNonce := ""
	if client != nil {
		// We have a client. Create a token that can be used for syncing.
		syncNonce, err = s.createSyncNonce(ctx, client)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	// Generate the link token.
	claims := linkTokenClaims{
		SyncNonce:   syncNonce,
		RedirectURL: params.RedirectURL,
	}
	token, err := s.generateLinkToken(claims, env.Instance.PrivateKey, env.Instance.KeyAlgorithm)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Build the URL to link the client to the satellite domain.
	location, err = buildHandshakeSatelliteSyncURL(satelliteDomain.FapiURL(), token)
	if err != nil {
		return nil, apierror.FormInvalidParameterValue(paramSatelliteFAPI, params.SatelliteDomain)
	}
	return location, nil
}

// Returns the https API route for linking clients on the provided domain.
func buildHandshakeSatelliteSyncURL(domain, token string) (*url.URL, error) {
	u, err := joinURL(domain, "/v1/client/handshake")
	if err != nil {
		return nil, fmt.Errorf("cannot build /v1/client/handshake link endpoint for domain %s: %w", domain, err)
	}

	q := u.Query()
	q.Add(paramSyncToken, token)
	u.RawQuery = q.Encode()
	return u, nil
}

type linkTokenClaims struct {
	josejwt.Claims

	SyncNonce   string `json:"sync_nonce"`
	RedirectURL string `json:"redirect_url"`
}

func (s *Service) generateLinkToken(claims linkTokenClaims, privKey, keyAlgo string) (string, error) {
	ttl := time.Second * time.Duration(constants.ExpiryTimeLong)
	claims.Expiry = josejwt.NewNumericDate(s.clock.Now().UTC().Add(ttl))

	token, err := jwt.GenerateToken(privKey, claims, keyAlgo)
	if err != nil {
		return token, fmt.Errorf("cannot sign jwt: %w", err)
	}
	return token, nil
}

func (s *Service) parseLinkToken(token, pubKey, keyAlgo string) (linkTokenClaims, error) {
	var claims linkTokenClaims

	parsedPubkey, err := pkiutils.LoadPublicKey([]byte(pubKey))
	if err != nil {
		return claims, fmt.Errorf("unable to parse instance public key: %w", err)
	}

	err = jwt.Verify(token, parsedPubkey, &claims, s.clock, keyAlgo)
	if err != nil {
		return claims, fmt.Errorf("cannot get claims: %w", err)
	}

	return claims, nil
}

// Returns the https API route for linking clients on the provided domain.
func buildLinkURL(domain, token string) (*url.URL, error) {
	u, err := joinURL(domain, "/v1/client/link")
	if err != nil {
		return nil, fmt.Errorf("cannot build /v1/client/link endpoint for domain %s: %w", domain, err)
	}

	q := u.Query()
	q.Add(paramLinkToken, token)
	u.RawQuery = q.Encode()
	return u, nil
}

func buildSyncURL(domain string, params syncParams) (*url.URL, error) {
	u, err := joinURL(domain, "/v1/client/sync")
	if err != nil {
		return nil, fmt.Errorf("cannot build /v1/client/sync endpoint for domain %s: %w", domain, err)
	}

	q := u.Query()
	q.Add(paramRedirectURL, params.RedirectURL)
	q.Add(paramLinkDomain, params.LinkDomain)
	u.RawQuery = q.Encode()
	return u, nil
}

func joinURL(domain, path string) (*url.URL, error) {
	endpoint, err := url.JoinPath(domain, path)
	if err != nil {
		return nil, fmt.Errorf("cannot join %s path for domain %s: %w", domain, path, err)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("cannot parse link URL %s for domain %s: %w", endpoint, domain, err)
	}
	u.Scheme = "https"
	return u, nil
}

func withSyncedQuery(u *url.URL) *url.URL {
	q := u.Query()
	q.Add(paramSynced, "true")
	u.RawQuery = q.Encode()
	return u
}

// Link will parse the provided token and return its redirect URL, along with
// a client, if one can be fetched by the token claims.
// Link operates on satellite domains only.
func (s *Service) Link(ctx context.Context, token string) (*model.Client, *url.URL, apierror.Error) {
	env := environment.FromContext(ctx)

	// Ensure that we're on a satellite domain.
	if !env.Domain.IsSatellite(env.Instance) {
		return nil, nil, apierror.InvalidHost()
	}

	claims, err := s.parseLinkToken(token, env.Instance.PublicKey, env.Instance.KeyAlgorithm)
	if err != nil {
		return nil, nil, apierror.FormInvalidParameterValue(paramLinkToken, token)
	}

	location, err := url.Parse(claims.RedirectURL)
	if err != nil {
		return nil, nil, apierror.FormInvalidParameterValue(paramRedirectURL, claims.RedirectURL)
	}

	// Find the client that we'll link to.
	var syncNonce *model.SyncNonce
	if maintenance.FromContext(ctx) {
		var nonceFromRedis model.SyncNonce
		if err := s.cache.Get(ctx, clerkmaintenance.SyncNonceKey(claims.SyncNonce, env.Instance.ID), &nonceFromRedis); err != nil {
			return nil, nil, apierror.Unexpected(err)
		} else if nonceFromRedis.SyncNonce != nil {
			syncNonce = &nonceFromRedis
		}
	} else {
		syncNonce, err = s.syncNonceRepo.QueryByNonceAndInstanceID(ctx, s.db, claims.SyncNonce, env.Instance.ID)
		if err != nil {
			return nil, nil, apierror.Unexpected(err)
		}
	}

	if syncNonce == nil {
		return nil, withSyncedQuery(location), nil
	}
	if syncNonce.Consumed {
		return nil, nil, apierror.SyncNonceAlreadyConsumed()
	}

	client, err := s.clientDataService.FindClient(ctx, env.Instance.ID, syncNonce.ClientID)
	if err != nil {
		return nil, nil, apierror.Unexpected(err)
	}

	syncNonce.Consumed = true
	if maintenance.FromContext(ctx) {
		err = s.cache.Set(ctx, clerkmaintenance.SyncNonceKey(syncNonce.Nonce, syncNonce.InstanceID), syncNonce, time.Hour)
	} else {
		err = s.syncNonceRepo.UpdateConsumed(ctx, s.db, syncNonce)
	}
	if err != nil {
		return nil, nil, apierror.Unexpected(err)
	}

	return client.ToClientModel(), withSyncedQuery(location), nil
}

func (s *Service) GetPrimaryDomainHandshakeURL(ctx context.Context, redirectURL string) (handshakeURL string, err error) {
	env := environment.FromContext(ctx)

	// If we're on a satellite domain, redirect to the primary so we can
	// sync with that. Pass the satellite domain as a query parameter so
	// that main knows where the request came from.
	primaryDomain, err := s.domainRepo.FindByID(ctx, s.db, env.Instance.ActiveDomainID)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	primaryHandshakeURLRaw, err := url.JoinPath(primaryDomain.FapiURL(), "/v1/client/handshake")
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	primaryHandshakeURL, err := url.Parse(primaryHandshakeURLRaw)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	return urlUtils.AddQueryParameters(primaryHandshakeURL.String(),
		urlUtils.NewQueryStringParameter(paramRedirectURL, redirectURL),
		urlUtils.NewQueryStringParameter(paramSatelliteFAPI, env.Domain.Name),
	)
}

func (s *Service) createHandshakeSessionCookie(ctx context.Context, client *model.Client) (*http.Cookie, error) {
	session, err := s.getActiveSessionForClient(ctx, client)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if session == nil {
		// If no session is found, unset the session cookie on return of the handshake
		return fapicookies.NewSessionClear(ctx), nil
	}

	ctxWithSession := requesting_session.NewContext(ctx, session)
	sessionToken, err := s.tokensService.CreateSessionToken(ctxWithSession, session.ID)
	if err != nil {
		return nil, apierror.InvalidHandshake("unable to create session token")
	}

	return fapicookies.NewSessionSignedIn(ctxWithSession, sessionToken.JWT), nil
}

// CreateHandshakeCookieJar creates common handshake cookies, client_uat and __session, and returns a cookie jar containing them.
func (s *Service) CreateHandshakeCookieJar(ctx context.Context, cookieOven *cookies.Oven, client *model.Client, handshakeRedirectURL *url.URL, devBrowserCookie *http.Cookie) (cookies.Jar, apierror.Error) {
	env := environment.FromContext(ctx)

	// Cleanup clerk-js v4 client_uat
	cookieJar := []*http.Cookie{fapicookies.NewClientUatClear()}

	// Generate updated client_uat cookie
	clientUatDomain, err := s.fapiCookieService.ClientUatDomain(env, handshakeRedirectURL)
	if err != nil {
		return nil, apierror.FormInvalidParameterValue(paramRedirectURL, handshakeRedirectURL.String())
	}

	clientUatCookies, err := s.fapiCookieService.CreateClientUatCookies(ctx, cookieOven, client, clientUatDomain)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	cookieJar = append(cookieJar, clientUatCookies...)

	sessionCookie, err := s.createHandshakeSessionCookie(ctx, client)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	cookieJar = append(cookieJar, cookieOven.Session(sessionCookie)...)

	if devBrowserCookie != nil {
		cookieJar = append(cookieJar, cookieOven.DevBrowser(devBrowserCookie)...)
	}

	return cookieJar, nil
}

// Check the request's clerkjs_version to determine if the requesting client supports the handshake flow.
func (s *Service) IsRequestEligibleForHandshake(clerkJSVersion string) bool {
	return !versions.IsBefore(clerkJSVersion, cenv.Get(cenv.HandshakeClerkJSVersion), true)
}

// Generate Handshake token and set in response cookies for production instances or
// in redirect url query params for development instances
func (s *Service) SetHandshakeTokenInResponse(ctx context.Context,
	w http.ResponseWriter, client *model.Client, redirectURL string,
	clientType client_type.ClientType, clerkJSVersion string) (string, apierror.Error) {
	parsedRedirectURL, err := url.Parse(redirectURL)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	// We override DefaultRecipes to BaseRecipe to avoid returning both suffixed and
	// un-suffixed cookies by default. Returning both cookies will cause the response
	// to fail due to client request header size limit.
	cookieOven := fapicookies.NewOven(ctx, []cookies.Recipe{&cookies.BaseRecipe{}})

	// We only need to set the handshake cookie in non-native flows, other platforms should get the session ID back and handle token creation.
	if clientType.IsNative() || !s.IsRequestEligibleForHandshake(clerkJSVersion) {
		return redirectURL, nil
	}
	cookieJar, apiErr := s.CreateHandshakeCookieJar(ctx, cookieOven, client, parsedRedirectURL, nil)
	if apiErr != nil {
		return "", apiErr
	}
	// Create encoded handshake cookie and set it on the response
	handshakeCookie, err := s.fapiCookieService.CreateEncodedHandshakeCookie(ctx, cookieJar)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	env := environment.FromContext(ctx)
	if env.Instance.IsProduction() {
		// The handshake cookie is used to ensure the newly created session is available on the server.
		// Without this cookie, apps that leverage server-side rendering would treat the request as signed out.
		http.SetCookie(w, handshakeCookie)
	} else {
		q := parsedRedirectURL.Query()
		q.Set(handshakeCookie.Name, handshakeCookie.Value)
		parsedRedirectURL.RawQuery = q.Encode()
	}

	return parsedRedirectURL.String(), nil
}

// Validates that the provided redirect_url is associated with the instance.
func (s *Service) ValidateRedirectURL(ctx context.Context, redirectURL *url.URL) apierror.Error {
	env := environment.FromContext(ctx)

	if redirectURL.Host == "" {
		return apierror.FormInvalidParameterValue(paramRedirectURL, "")
	}

	if !env.Instance.IsProduction() {
		return nil
	}

	if redirectURL == nil {
		return apierror.FormInvalidParameterValue(paramRedirectURL, "")
	}

	domainToCheck := redirectURL.Hostname()
	hostParts := strings.Split(domainToCheck, ".")

	// Check the TLD+1 first, as it's most likely to be the domain we're looking for.
	eTLD, err := psl.Domain(domainToCheck)

	// If error, this is a TLD so we know the domain can't be associated.
	if err != nil {
		return apierror.FormInvalidParameterValue(paramRedirectURL, redirectURL.String())
	}

	associatedDomain, err := s.domainRepo.QueryByNameAndInstance(ctx, s.db, eTLD, env.Instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	// If the TLD+1 is associated with the instance, there's nothing else we need to do.
	if associatedDomain != nil {
		return nil
	}

	// Otherwise, if the hostname contains subdomains, walk up to the root domain a maximum of three times to see if there is a match.
	// We have the hard limit of three to prevent potential abuse or unintended triggering of a large number of queries.
	for i := 0; i < len(hostParts) && i < 3; i++ {
		domainToCheck = strings.Join(hostParts[i:], ".")

		if domainToCheck == eTLD {
			// If we reach this, we've already checked TLD+1 above and so we know the redirect_url is invalid.
			break
		}

		// Validate that the redirect URL's hostname matches a domain associated with the instance.
		domain, err := s.domainRepo.QueryByNameAndInstance(ctx, s.db, domainToCheck, env.Instance.ID)
		if err != nil {
			return apierror.Unexpected(err)
		}

		if domain != nil {
			associatedDomain = domain
			break
		}
	}

	// Finally, the redirect_url is definitively not associated with the instance
	if associatedDomain == nil {
		return apierror.FormInvalidParameterValue(paramRedirectURL, redirectURL.String())
	}

	return nil
}

func (s *Service) getActiveSessionForClient(ctx context.Context, client *model.Client) (*model.Session, error) {
	if client == nil {
		return nil, nil
	}

	env := environment.FromContext(ctx)

	// You're not authenticated as anyone. Please prove you are who you say you are.
	// We need to determine the latest active session since in multi-session applications,
	// there might be several sessions present in the same Client.
	session, err := s.clientDataService.QueryLatestTouchedActiveSessionByClient(ctx, env.Instance.ID, client.ID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}
	return session.ToSessionModel(), nil
}

// Creates a sync token that can be used for client syncing between domain.
func (s *Service) createSyncNonce(ctx context.Context, client *model.Client) (string, error) {
	randomToken, err := rand.Token()
	if err != nil {
		return "", err
	}
	// include ksuid to eliminate clash possibilities
	nonce := fmt.Sprintf("%s_%s", randomToken, ksuid.New())

	syncNonce := &model.SyncNonce{
		SyncNonce: &sqbmodel.SyncNonce{
			InstanceID: client.InstanceID,
			ClientID:   client.ID,
			Nonce:      nonce,
		},
	}
	if maintenance.FromContext(ctx) {
		err = s.cache.Set(ctx, clerkmaintenance.SyncNonceKey(nonce, client.InstanceID), syncNonce, time.Hour)
	} else {
		err = s.syncNonceRepo.Insert(ctx, s.db, syncNonce)
	}
	return nonce, err
}
