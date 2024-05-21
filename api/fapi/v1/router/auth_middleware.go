package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"clerk/api/apierror"
	"clerk/api/shared/strategies"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/devbrowser"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/jwtcontext"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/jwt"
	clerkmaintenance "clerk/pkg/maintenance"
	"clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/pkg/versions"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/log"
	pkiutils "clerk/utils/pki"
	"clerk/utils/response"

	"clerk/pkg/psl"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

// csrfCheck implements CSRF protection and populates the result of the check in
// r.Context.
//
// See https://owasp.org/www-community/attacks/csrf.
func csrfCheck(clock clockwork.Clock) func(http.ResponseWriter, *http.Request) (*http.Request, apierror.Error) {
	return func(w http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		ctx := r.Context()
		env := environment.FromContext(ctx)

		csrfForm := r.Form.Get("_csrf_token")
		csrfCookie, err := r.Cookie("__ck_csrf_token")
		if err == nil {
			response.UnsetCSRFToken(w, env.Domain.Name, clock)
		}

		if err != nil || csrfForm == "" || csrfCookie.Value != csrfForm {
			ctx = context.WithValue(ctx, ctxkeys.CSRFPresentAndValid, false)
		} else {
			ctx = context.WithValue(ctx, ctxkeys.CSRFPresentAndValid, true)
		}

		return r.WithContext(ctx), nil
	}
}

// corsFirefoxFix resolves an issue where Firefox sends a populated Origin header after
// redirect instead of sending a "null" Origin header.  In this case, Firefox still
// expects the response "Access-Control-Allow-Origin" header to be set to "null", but
// chi-cors automatically sets whatever value is in the Origin header.  So this method
// forces Access-Control-Allow-Origin to respond with "null" in the event of a redirect,
// which we verify by checking if CSRFPresentAndValid
// Bug report: https://bugzilla.mozilla.org/show_bug.cgi?id=1649888
func corsFirefoxFix(w http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	csrfChecked := ctx.Value(ctxkeys.CSRFPresentAndValid).(bool)
	if csrfChecked {
		w.Header().Del("access-control-allow-origin")
		w.Header().Set("access-control-allow-origin", "null")
	}
	return r.WithContext(ctx), nil
}

// validateRequestOrigin ensures that requests are coming from expected sources.
// If not, it responds with HTTP 401.
//
// For example, https://example.com is considered a valid origin for
// clerk.example.com, while http://example.com and https://bar.com are
// considered invalid.
//
// It is a security mechanism similar to the same-origin policy, that tries to
// reduce possible attack vectors.
func validateRequestOrigin(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	// these paths need to be invoked directly in the browser in order to
	// complete OAuth, Magic Link, Ticket or Multi-domain Sync flows.
	ignoredExactPaths := set.New(
		"/v1/client/link",
		"/v1/client/sync",
		"/v1/client/handshake",
		"/v1/dev_browser/init",
		"/v1/tickets/accept",
		"/v1/oauth_callback",
		"/v1/verify",
		"/v1/health",
		"/v1/proxy-health",
		"/.well-known/",
		"/")

	if ignoredExactPaths.Contains(r.URL.Path) {
		return r.WithContext(ctx), nil
	}

	ignoredSubPaths := set.New(
		"/v1/saml/acs/",
	)

	for _, path := range ignoredSubPaths.Array() {
		if strings.HasPrefix(r.URL.Path, path) {
			return r.WithContext(ctx), nil
		}
	}

	proxyURLHeaders, proxyURLHeadersPresent := r.Header[constants.ClerkProxyURL]
	originHeaders, originHeadersPresent := r.Header["Origin"]
	authorizationHeaders, authorizationHeadersPresent := r.Header["Authorization"]

	var origin string
	if originHeadersPresent {
		origin = originHeaders[0]
	} else if proxyURLHeadersPresent && r.Method == http.MethodGet {
		proxyURL, err := url.Parse(proxyURLHeaders[0])
		if err != nil {
			log.Warning(ctx, "fapi/auth_middleware: validateRequestOrigin: origin: %s, proxyURLHeaders: %s", origin, proxyURLHeaders)
			return r.WithContext(ctx), apierror.InvalidHost()
		}

		origin = fmt.Sprintf("%s://%s", proxyURL.Scheme, proxyURL.Host)
	}

	// instead of checking for the origin value to exist, we check for the header
	// to block production instances with empty origin header - found in Test_validateRequestOrigin test case
	originHeaderPresent := proxyURLHeadersPresent || originHeadersPresent

	// Browser-like stacks
	//
	// For browser-like stacks such as Capacitor, Browser extensions, or Electron apps users need to
	// manually configure the request blocker by updating the allowed_origins for their current instance. Instead of relying on the default XOR logic of the request blocker, the provided
	// origin is tested against a predefined list of allowed origins. If it matches,
	// the request passes.
	//
	// In such environments, the origin header will always be present. For example the origin
	// is capacitor://localhost for all Capacitor hybrid applications. For chrome extensions
	// popup, background or service worker pages the origin is chrome-extension://extension_id_goes_here. For Electron apps the origin be default is http://localhost:3000.
	//
	//
	// Allowed origins can be used when a Clerk powered application is hosted in an iframe in Safari. Safari blocks
	// cookie dropping in iframes also for first-party SameSite=None cookies. So the Clerk FAPI can't set the __client cookie.
	// For such cases, we can suggest to customers to use Clerk with HTTP Authorization headers but enforce a
	// very strict CSP to prevent XSS. [SwiftApp](https://runswiftapp.com) runs in this mode in production.
	//
	// https://webkit.org/blog/10218/full-third-party-cookie-blocking-and-more/
	// https://community.shopify.com/c/shopify-apis-and-sdks/safari-13-1-and-embedded-apps/m-p/693186#M47026
	// https://stackoverflow.com/questions/52173595/how-to-debug-safari-itp-2-0-requeststorageaccess-failure
	if originHeaderPresent {
		for _, allowedOrigin := range env.Instance.AllowedOrigins {
			if allowedOrigin == origin {
				return r.WithContext(ctx), nil
			}
		}
	}

	// sanity checks
	if originHeaderPresent && authorizationHeadersPresent {
		return r.WithContext(ctx), apierror.OriginAndAuthorizationMutuallyExclusive()
	}
	if len(originHeaders) > 1 {
		return r.WithContext(ctx), apierror.MultipleOriginHeaderValues()
	}
	if len(authorizationHeaders) > 1 {
		return r.WithContext(ctx), apierror.MultipleAuthorizationHeaderValues()
	}

	// we don't check this for native flows, since it's expected that our native
	// SDKs emit an empty auth & origin header, during the initial bootstrapping
	if client_type.FromContext(ctx).IsBrowser() && !originHeaderPresent && !authorizationHeadersPresent {
		if !cenv.IsEnabled(cenv.FlagLogOriginMissingRequestHeaders) {
			return r.WithContext(ctx), apierror.MissingRequestHeaders(client_type.FromContext(ctx))
		}
		log.Info(ctx, "Browser request does not have origin or authorization header: %s", env.Domain.Name)
	}

	if env.Instance.IsProduction() && originHeaderPresent {
		csrfValid := ctx.Value(ctxkeys.CSRFPresentAndValid).(bool)

		if origin == "null" && csrfValid {
			return r.WithContext(ctx), nil
		}

		originDomain := strings.TrimPrefix(origin, "https://")

		if strings.HasPrefix(origin, "https://") &&
			(originDomain == env.Domain.Name || strings.HasSuffix(origin, "."+env.Domain.Name)) {
			return r.WithContext(ctx), nil
		}

		return r.WithContext(ctx), apierror.InvalidOriginHeader()
	}

	return r.WithContext(ctx), nil
}

// httpMethodPolyfill allows for converting requests from (GET or POST) to (PUT PATCH POST and DELETE).
// To use, pass a _method field equal to PUT PATCH or DELETE
// If the incoming request is a GET, a CSRF token must be present and valid.
func httpMethodPolyfill(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	rctx := chi.RouteContext(r.Context())
	newMethod := strings.ToUpper(r.Form.Get("_method"))
	delete(r.Form, "_method")
	if newMethod == "PUT" || newMethod == "PATCH" || newMethod == "DELETE" || newMethod == "POST" {
		csrfChecked := ctx.Value(ctxkeys.CSRFPresentAndValid).(bool)
		if r.Method == "POST" || (r.Method == "GET" && csrfChecked) {
			rctx.RouteMethod = newMethod
		}
	}

	return r.WithContext(ctx), nil
}

func setDevBrowserRequestContext(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	clientType := client_type.FromContext(ctx)

	if !env.Instance.IsDevelopmentOrStaging() || !clientType.IsBrowser() {
		return r, nil
	}

	eTLDPlusOne, err := psl.DomainForDevBrowser(env.Domain.Name)
	if err != nil {
		return r, apierror.Unexpected(err)
	}

	origin := r.Header.Get("Origin")
	if origin == "" || strings.HasSuffix(origin, "."+eTLDPlusOne) {
		// Old-style domains: for happy.hippo.lcl.dev, the eTLD+1 is
		// happy.hippo.lcl.dev because *.lcl.dev is in the Public Suffix List.
		// - https://github.com/publicsuffix/list/blob/0ed17ee161ed2ae551c78f3b399ac8f2724d2154/public_suffix_list.dat#L12375-L12382
		//
		// Therefore, if origin=accounts.happy.hippo.lcl.dev and it requests FAPI at
		// clerk.happy.hippo.lcl.dev, then we consider this a first-party context,
		// because both eTLDs+1 equal to happy.hippo.lcl.dev.
		//
		// New-style domains: for happy-hippo.accounts.dev, the eTLD+1 is
		// accounts.dev because accounts.dev is not in the Public Suffix List.
		// Therefore, if origin=happy-hippo.accounts.dev and it requests FAPI at
		// happy-hippo.clerk.accounts.dev, then we consider this a first-party
		// request, because both eTLDs+1 equal to accounts.dev.
		ctx = devbrowser.NewContext(ctx, devbrowser.FirstParty)
	} else {
		ctx = devbrowser.NewContext(ctx, devbrowser.ThirdParty)
	}
	return r.WithContext(ctx), nil
}

// parseAuthToken parses the authentication token and injects the verified
// claims (if any) to the request context.
func parseAuthToken(clock clockwork.Clock) clerkhttp.MiddlewareFunc {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		ctx := r.Context()
		env := environment.FromContext(ctx)
		instance := env.Instance
		clientType := client_type.FromContext(ctx)
		devBrowserRequestContext := devbrowser.FromContext(ctx)

		var token string

		if clientType.IsNative() {
			token = r.Header.Get("Authorization")
		} else if clientType.IsBrowser() {
			if devBrowserRequestContext.IsThirdParty() || instance.UsesURLBasedSessionSyncingMode(env.AuthConfig) {
				token = r.URL.Query().Get(constants.DevBrowserQueryParam)

				// Fallback to the legacy query param if the new one is not present
				if token == "" {
					token = r.URL.Query().Get(constants.DevSessionQueryParam)
				}

				q := r.URL.Query()
				q.Del(constants.DevSessionQueryParam)
				q.Del(constants.DevBrowserQueryParam)
				r.URL.RawQuery = q.Encode()
			} else { // cookie-based mode
				jwtCookie, err := r.Cookie(constants.ClientCookie)
				if err == nil {
					token = jwtCookie.Value
				}
			}
		}

		// remove it otherwise the subsequent parameter validations in the
		// handlers will fail
		delete(r.Form, constants.DevSessionQueryParam)
		delete(r.PostForm, constants.DevSessionQueryParam)
		delete(r.Form, constants.DevBrowserQueryParam)
		delete(r.PostForm, constants.DevBrowserQueryParam)

		if token == "" {
			return r.WithContext(ctx), nil
		}

		pubkey, err := pkiutils.LoadPublicKey([]byte(instance.PublicKey))
		if err != nil {
			return r.WithContext(ctx), nil
		}

		claims := make(map[string]interface{})
		err = jwt.Verify(token, pubkey, &claims, clock, instance.KeyAlgorithm)
		if err != nil {
			return r.WithContext(ctx), nil
		}

		return r.WithContext(jwtcontext.NewContext(ctx, claims)), nil
	}
}

// fetchDevSessionIfNecessary is a middleware which checks whether it needs to fetch
// the dev session from the FE.
// This is needed for the case of url-based session syncing and flows like magic links,
// or organization invitations, where users invoke FAPI directly without going via
// ClerkJS. In these cases, and only when url-based session syncing is used, FAPI doesn't
// know anything about the caller...so, given that there are no cookies in play, it needs
// to redirect to FE and ask for the dev session.
func fetchDevSessionIfNecessary(deps clerk.Deps) clerkhttp.MiddlewareFunc {
	return func(w http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		ctx := r.Context()
		env := environment.FromContext(ctx)

		requestVersion := clerkjs_version.FromContext(ctx)
		if !versions.IsValid(requestVersion) || versions.IsBefore(requestVersion, cenv.Get(cenv.FetchDevSessionFromFEClerkJSVersion), true) {
			// We probably don't have the required changes in the Frontend to support this, so
			// we don't redirect to fetch dev session.
			return r, nil
		}

		devBrowser := requestingdevbrowser.FromContext(ctx)
		if !env.Instance.UsesURLBasedSessionSyncingMode(env.AuthConfig) || devBrowser != nil {
			return r, nil
		}

		// Find the dev browser that initiated the request which resulted in
		// this FAPI request.
		devBrowser, err := findDevBrowserFromRequest(ctx, deps, r, env.Instance)
		if err != nil {
			if apiErr, isAPIErr := apierror.As(err); isAPIErr {
				return nil, apiErr
			}
			return nil, apierror.Unexpected(err)
		}

		// redirect to sign in url, then replay /authorize request
		origin := env.Instance.Origin(env.Domain, devBrowser)
		accountsURL := env.Domain.AccountsURL()
		signInURL := env.DisplayConfig.Paths.SignInURL(origin, accountsURL)

		authorizeRequest := url.URL{
			Scheme:   "https",
			Host:     env.Domain.AuthHost(),
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}

		redirectTo := fmt.Sprintf("%s?redirect_url=%s", signInURL, url.QueryEscape(authorizeRequest.String()))

		http.Redirect(w, r, redirectTo, http.StatusFound)
		return nil, nil
	}
}

func findDevBrowserFromRequest(ctx context.Context, deps clerk.Deps, r *http.Request, instance *model.Instance) (*model.DevBrowser, error) {
	token := r.URL.Query().Get("token")
	if token == "" {
		return nil, nil
	}
	claims, err := strategies.ParseVerificationLinkToken(token, instance.PublicKey, instance.KeyAlgorithm, deps.Clock())
	// Ignore expiration errors, they will be handled later in the flow
	if err != nil && !errors.Is(err, jwt.ErrTokenExpired) {
		return nil, apierror.VerificationInvalidLinkToken()
	}

	if claims.DevBrowserID == "" {
		return nil, nil
	}

	if maintenance.FromContext(ctx) {
		var devBrowser model.DevBrowser
		if err := deps.Cache().Get(ctx, clerkmaintenance.DevBrowserKey(claims.DevBrowserID, instance.ID), &devBrowser); err != nil {
			sentry.CaptureException(ctx, err)
		} else if devBrowser.DevBrowser != nil {
			return &devBrowser, nil
		}
	}
	return repository.NewDevBrowser().QueryByIDAndInstance(ctx, deps.DB(), claims.DevBrowserID, instance.ID)
}
