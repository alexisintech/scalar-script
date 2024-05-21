package clients

import (
	"encoding/json"
	"net/http"
	"net/url"

	"clerk/api/apierror"
	fapicookies "clerk/api/fapi/v1/cookies"
	"clerk/api/fapi/v1/dev_browser"
	"clerk/api/fapi/v1/wrapper"
	"clerk/model"
	"clerk/pkg/cache"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/cookies"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_session"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/ctxkeys"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/form"
	"clerk/utils/log"
	"clerk/utils/param"
	urlUtils "clerk/utils/url"
)

type HTTP struct {
	cache             cache.Cache
	db                database.Database
	devBrowserService *dev_browser.Service

	cookieSetter  *fapicookies.CookieSetter
	cookieService *fapicookies.Service
	clientService *Service
	wrapper       *wrapper.Wrapper
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		cache:             deps.Cache(),
		db:                deps.DB(),
		devBrowserService: dev_browser.NewService(deps),
		cookieSetter:      fapicookies.NewCookieSetter(deps),
		cookieService:     fapicookies.NewService(deps),
		clientService:     NewService(deps),
		wrapper:           wrapper.NewWrapper(deps),
	}
}

// Middleware /v1
func (h *HTTP) SetRequestingClient(w http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	rotatingTokenNonce := r.Context().Value(ctxkeys.RotatingTokenNonce).(string)
	newCtx, err := h.clientService.SetRequestingClient(r.Context(), rotatingTokenNonce)
	if err != nil {
		return r, err
	}

	client, hasClient := newCtx.Value(ctxkeys.RequestingClient).(*model.Client)
	if hasClient && client != nil {
		// We initialize the session context with a nil value here,
		// in order to create a pointer reference that encloses the model.Session object.
		// Up to this stage, the request context is accessible to the clientUatResponseWriter object
		// through its req struct field.
		// Once SetRequestingSesion updates the reference to point to a model.Session instance, the UAT cookie writer will be
		// able to retrieve the up-to-date value since the pointer to the enclosing reference object remains shared.
		// https://github.com/clerk/clerk_go/blob/dd4adefb1e37b84b4b94428a8af778947748fa30/api/fapi/v1/cookies/client_uat.go#L71
		newCtx = requesting_session.NewContext(newCtx, nil)
	}

	dvb := requestingdevbrowser.FromContext(newCtx)
	isDevBrowser := dvb != nil

	if !isDevBrowser {
		return r.WithContext(newCtx), nil
	}

	// With DevBrowser, there are two cookies we expect to get out of sync
	// 1. A cookie on the developer's host (usually localhost)
	// 2. A cookie on clerk.*.lcl.dev

	// The DevBrowser ID in the cookie on both hosts is held constant, so
	// we leverage the Client API (clerk.*.lcl.dev) to keep both cookies in
	// sync.

	// Any request made to the Client API returns the latest cookie across
	// both hosts.

	env := environment.FromContext(newCtx)
	if hasClient && client != nil {
		_ = fapicookies.SetClientCookie(newCtx, h.db, h.cache, w, client, env.Domain.AuthHost())
	} else {
		_ = fapicookies.UnsetClientCookie(newCtx, h.db, h.cache, w, env.Domain.AuthHost())
	}

	return r.WithContext(newCtx), nil
}

// Middleware /v1
func (h *HTTP) UpdateClientCookieIfNeeded(w http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	client, _ := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	if client == nil {
		return r, nil
	}

	updateCookieResponse, err := h.clientService.UpdateClientCookieIfNeeded(ctx)
	if err != nil {
		return nil, err
	}

	if updateCookieResponse == nil {
		// no update was needed, we continue the processing of the request
		return r, nil
	}

	response, err := h.cookieSetter.RespondWithCookie(ctx, w, r, updateCookieResponse.Client, updateCookieResponse.Body, nil)
	if err != nil {
		return nil, err
	}

	goerr := json.NewEncoder(w).Encode(response)
	if goerr != nil {
		return nil, apierror.Unexpected(goerr)
	}

	return nil, nil
}

// Middleware
func (h *HTTP) VerifyRequestingClient(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()

	client, _ := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	if client == nil {
		return r, apierror.SignedOut()
	}

	return r, nil
}

// GET /v1/client
func (h *HTTP) Read(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	client, err := h.clientService.Read(ctx)
	if err != nil {
		return nil, err
	}
	return h.wrapper.WrapResponse(ctx, client, nil)
}

// PUT /v1/client
// POST /v1/client
func (h *HTTP) Create(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	pl := param.NewList(param.NewSet(), param.NewSet())
	formErrs := form.Check(r.Form, pl)
	if formErrs != nil {
		return nil, formErrs
	}

	newClient, err := h.clientService.Create(ctx)
	if err != nil {
		return nil, err
	}

	// add new client to the logline so that it's included in our logs
	log.AddToLogLine(ctx, log.ClientID, newClient.ID)
	log.AddToLogLine(ctx, log.RotatingToken, newClient.RotatingToken)

	return h.cookieSetter.RespondWithCookie(ctx, w, r, newClient, nil, nil)
}

// DELETE /v1/client
func (h *HTTP) Delete(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	cookieErr := fapicookies.UnsetClientCookie(ctx, h.db, h.cache, w, env.Domain.AuthHost())
	if cookieErr != nil {
		return nil, apierror.Unexpected(cookieErr)
	}

	fapicookies.UnsetClientHandshakeCookie(ctx, w, env.Domain.Name)

	deletedClient, err := h.clientService.Delete(ctx)
	if err != nil {
		return nil, err
	}

	return h.wrapper.WrapResponse(ctx, deletedClient, nil)
}

const (
	paramRedirectURL   = "redirect_url"
	paramLinkDomain    = "link_domain"
	paramLinkToken     = "__clerk_token"
	paramSatelliteFAPI = "satellite_fapi"
	paramSyncToken     = "__clerk_sync_token" // #nosec G101
	paramSynced        = "__clerk_synced"
)

// GET /v1/client/sync
func (h *HTTP) Sync(w http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	location, err := h.clientService.Sync(r.Context(), syncParams{
		LinkDomain:  r.URL.Query().Get(paramLinkDomain),
		RedirectURL: r.URL.Query().Get(paramRedirectURL),
	})
	if err != nil {
		return nil, err
	}

	clerkhttp.Redirect(w, r, location.String(), http.StatusTemporaryRedirect)
	return nil, nil
}

// GET /v1/client/link
func (h *HTTP) Link(w http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	client, location, err := h.clientService.Link(ctx, r.URL.Query().Get(paramLinkToken))
	if err != nil {
		return nil, err
	}

	// Set all client related cookies
	cookieOven := fapicookies.NewOvenWithDefaultRecipes(ctx)
	cookieJar, cookieErr := h.cookieService.CreateCookiesForClient(ctx, cookieOven, client)
	if cookieErr != nil {
		return nil, apierror.Unexpected(cookieErr)
	}
	for _, c := range cookieJar {
		http.SetCookie(w, c)
	}

	clerkhttp.Redirect(w, r, location.String(), http.StatusTemporaryRedirect)
	return nil, nil
}

// GET /v1/client/handshake
func (h *HTTP) Handshake(w http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	// Multi-domain syncing: satellite domain receives sync token from primary domain
	isSatelliteSyncFromPrimary := r.URL.Query().Get(paramSyncToken) != "" && env.Domain.IsSatellite(env.Instance)
	// Multi-domain syncing: primary domain receives sync request from satellite domain
	//
	// satellite_fapi is required since the flow is the following
	//
	// app.satellite.com
	// clerk.satellite.com?redirect_url=app.satellite.com
	// clerk.primary.com?satellite_domain=clerk.satellite.com&redirect_url=app.satellite.com
	// clerk.satellite.com?redirect_url=app.satellite.com
	// app.satellite.com

	isPrimarySyncingFromSatellite := r.URL.Query().Get(paramSatelliteFAPI) != "" && env.Domain.IsPrimary(env.Instance)

	// The redirect URL is always required for a handshake, except for when a satellite domain is syncing from primary domain.
	// In this case, the redirect URL is embedded in the sync token.
	isRedirectURLRequired := !isSatelliteSyncFromPrimary
	redirectURLRaw := r.URL.Query().Get(paramRedirectURL)

	if isRedirectURLRequired && redirectURLRaw == "" {
		return ctx, apierror.InvalidHandshake("missing redirect_url")
	}

	redirectURL, err := url.ParseRequestURI(redirectURLRaw)
	if err != nil && isRedirectURLRequired {
		return ctx, apierror.FormInvalidParameterValue(paramRedirectURL, redirectURLRaw)
	}

	// We validate the redirect URL scheme here instead of in ValidateRedirectURL because we need this to happen as long
	// Revisit once we refactor how we do redirects across the board.
	if redirectURLRaw != "" && !(redirectURL.Scheme == "https" || redirectURL.Scheme == "http" || redirectURL.Scheme == "") {
		return ctx, apierror.InvalidHandshake("invalid url scheme")
	}

	if isRedirectURLRequired {
		redirectURLErr := h.clientService.ValidateRedirectURL(ctx, redirectURL)
		if redirectURLErr != nil {
			return nil, redirectURLErr
		}
	}

	finalRedirectURL := ""
	if redirectURL != nil {
		finalRedirectURL = redirectURL.String()
	}

	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	// We override DefaultRecipes to BaseRecipe to avoid returning both suffixed and
	// un-suffixed cookies by default. Returning both cookies will cause the response
	// to fail due to client request header size limit.
	cookieOven := fapicookies.NewOven(ctx, []cookies.Recipe{&cookies.BaseRecipe{}})

	if env.Instance.IsProduction() {
		// Multi-domain syncing: initiate a satellite -> primary sync
		if env.Domain.IsSatellite(env.Instance) && !isSatelliteSyncFromPrimary {
			finalPrimaryHandshakeURL, err := h.clientService.GetPrimaryDomainHandshakeURL(ctx, finalRedirectURL)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}

			redirectWithCORS(w, r, finalPrimaryHandshakeURL, http.StatusTemporaryRedirect)
			return nil, nil
		}

		// Multi-domain syncing: primary domain receives sync request from satellite domain
		if isPrimarySyncingFromSatellite {
			syncLocation, err := h.clientService.PrimarySync(ctx, PrimarySyncParams{
				RedirectURL:     finalRedirectURL,
				SatelliteDomain: r.URL.Query().Get(paramSatelliteFAPI),
			})
			if err != nil {
				return nil, err
			}

			redirectWithCORS(w, r, syncLocation.String(), http.StatusTemporaryRedirect)
			return nil, nil
		}

		// Multi-domain syncing: finish satellite sync, receive the sync token from the primary domain
		// NOTE: This is the only multi-domain sync flow where we complete the full handshake, the cases above terminate the handshake early.
		if isSatelliteSyncFromPrimary {
			var syncErr apierror.Error

			finalRedirectURL, client, syncErr = h.syncSatelliteFromPrimary(w, r, cookieOven)
			if syncErr != nil {
				return nil, syncErr
			}
			if finalRedirectURL == "" {
				return nil, apierror.InvalidHandshake("no redirect URL received from satellite sync")
			}
		}
	}

	var devBrowserCookie *http.Cookie
	// Append dev browser token to sync session state
	if env.Instance.IsDevelopment() {
		var devBrowserToken string
		devBrowser := requestingdevbrowser.FromContext(ctx)
		hasDevBrowser := devBrowser != nil

		// If no DevBrowser is found, create a new one to send back with the completed handshake
		if hasDevBrowser {
			devBrowserToken = devBrowser.Token
		} else {
			devBrowser, err = h.devBrowserService.CreateDevBrowserModel(ctx, h.db, env.Instance, nil)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}
			devBrowserToken = devBrowser.Token
		}

		devBrowserCookie = fapicookies.NewDevBrowser(devBrowserToken)

		if err = h.devBrowserService.UpdateHomeOrigin(ctx, devBrowser, redirectURL.String()); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	cookieJar, apiErr := h.clientService.CreateHandshakeCookieJar(ctx, cookieOven, client, redirectURL, devBrowserCookie)
	if apiErr != nil {
		return "", apiErr
	}
	// Create encoded handshake cookie and set it on the response
	handshakeCookie, err := h.cookieService.CreateEncodedHandshakeCookie(ctx, cookieJar)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	// Complete the handshake, returning the handshake payload either in a cookie for production, or a query parameter for development.
	if env.Instance.IsProduction() {
		// For production instances, we are limited by the cookie size limit, which is 4kb in most (all?) browsers.
		// Log a warning if the handshake payload exceeds this limit
		handshakePayloadSizeInBytes := len(handshakeCookie.Value)
		if handshakePayloadSizeInBytes > 4096 {
			log.Warning(ctx, "client/handshake: handshake payload value exceeds browser cookie size limit: %d bytes (handshake_payload_too_big)", handshakePayloadSizeInBytes)
		}
		http.SetCookie(w, handshakeCookie)
	} else {
		handshakeQueryParameters := []urlUtils.QueryStringParameter{}

		handshakeQueryParameters = append(
			handshakeQueryParameters,
			urlUtils.NewQueryStringParameter(handshakeCookie.Name, handshakeCookie.Value),
		)

		// In development, pass the handshake value in a query parameter to facilitate the cross-origin handshake
		finalRedirectURL, err = urlUtils.AddQueryParameters(
			redirectURL.String(),
			handshakeQueryParameters...,
		)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	redirectWithCORS(w, r, finalRedirectURL, http.StatusTemporaryRedirect)
	return nil, nil
}

func (h *HTTP) syncSatelliteFromPrimary(w http.ResponseWriter, r *http.Request, cookieOven *cookies.Oven) (redirectURL string, client *model.Client, err apierror.Error) {
	ctx := r.Context()
	client, location, err := h.clientService.Link(ctx, r.URL.Query().Get(paramSyncToken))
	if err != nil {
		return "", nil, err
	}

	// Set all client related cookies
	cookieJar, cookieErr := h.cookieService.CreateCookiesForClient(ctx, cookieOven, client)
	if cookieErr != nil {
		return "", nil, apierror.Unexpected(cookieErr)
	}
	for _, c := range cookieJar {
		http.SetCookie(w, c)
	}

	return location.String(), client, nil
}

// Trigger a redirect with CORS headers that allow for subsequent redirects. The Origin header is null in these scenarios, so we need to explicitly allow it.
func redirectWithCORS(w http.ResponseWriter, r *http.Request, url string, code int) {
	// Set CORS headers
	w.Header().Add("Access-Control-Allow-Origin", "null")
	w.Header().Add("Access-Control-Allow-Credentials", "true")

	clerkhttp.Redirect(w, r, url, code)
}
