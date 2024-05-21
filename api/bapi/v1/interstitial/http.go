// In server-rendered apps (e.g. Rails, PHP), Clerk.js doesn't have the chance
// to run before the server constructs the response. Therefore, there are cases
// where the customer's backend isn't aware of the auth state (signed in, signed
// out etc.) of the request.
//
// To solve this problem, we introduce the interstitial page that is served by
// FAPI after a request from our SDK running in customer's backend. All in all
// this html page runs the clerk.js to synchronize the user state between FAPI
// and customer's backend. When this is completed, interstitial will redirect
// you back to the original page and the user state will be up-to-date.

// Refer to https://docs.google.com/document/d/1PGAykkmPjx5Mtdi6j-yHc5Qy-uasjtcXnfGKDy3cHIE
package interstitial

import (
	"fmt"
	"net/http"
	"regexp"

	"clerk/api/apierror"
	"clerk/api/shared/kima_hosts"
	"clerk/pkg/cenv"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/templates"
)

type HTTP struct{}

func NewHTTP() *HTTP {
	return &HTTP{}
}

// GET /v1/internal/interstitial
func (*HTTP) RenderPrivate(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	var data templates.InterstitialPageData

	fapiHost := env.Domain.AuthHost()
	if env.Instance.UsesKimaKeys() {
		data.UsesNewKeys = true
		data.PublishableKey = env.Instance.PublishableKey(env.Domain)
	} else { // uses legacy key
		data.UsesNewKeys = false
		data.FrontendAPI = fapiHost
	}

	data.ScriptURL = scriptURL(fapiHost, r)

	return nil, renderInterstitial(w, data)
}

// Next.js rewrites don't currently pass any headers set within a middleware to
// proxied requests. Hence, we cannot pass the Authorization header containing
// the CLERK_API_KEY value. For the time being we have no choice but to also
// expose this public interstitial endpoint that will render for whichever
// frontend_api or publishable_key query param passed to it.
//
// GET /v1/public/interstitial
func (*HTTP) RenderPublic(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	publishableKey := r.URL.Query().Get("publishable_key")

	frontendAPI := r.URL.Query().Get("frontend_api")
	legacyFrontendAPI := r.URL.Query().Get("frontendApi")
	proxyURL := r.URL.Query().Get("proxy_url")
	domain := r.URL.Query().Get("domain")
	signInURL := r.URL.Query().Get("sign_in_url")
	useDomainForScript := r.URL.Query().Get("use_domain_for_script") == "true"

	if legacyFrontendAPI != "" {
		frontendAPI = legacyFrontendAPI
	}

	if publishableKey == "" && frontendAPI == "" {
		return nil, apierror.MissingOneOfQueryParameters("publishable_key", "frontend_api")
	}

	var (
		fapiHost string
		data     templates.InterstitialPageData
		err      error
	)

	if publishableKey == "" {
		fapiHost = frontendAPI
		data.UsesNewKeys = false
		data.FrontendAPI = frontendAPI
	} else {
		fapiHost, err = kima_hosts.FapiDomainByPublishableKey(publishableKey)
		if err != nil {
			return nil, apierror.MalformedPublishableKey(publishableKey)
		}

		data.UsesNewKeys = true
		data.PublishableKey = publishableKey
	}

	data.ScriptURL = scriptURL(fapiHost, r)

	if proxyURL != "" {
		noSchemeMatcher := regexp.MustCompile("https?://")
		noSchemeProxyURL := noSchemeMatcher.ReplaceAllString(proxyURL, "")
		data.ScriptURL = scriptURL(noSchemeProxyURL, r)
		data.ProxyURL = proxyURL
	}

	if domain != "" {
		data.Domain = domain
		if useDomainForScript {
			data.ScriptURL = scriptURL("clerk."+domain, r)
		}
	}

	data.IsSatellite = r.URL.Query().Get("is_satellite") == "true"
	data.SignInURL = signInURL

	return nil, renderInterstitial(w, data)
}

func scriptURL(fapiHost string, r *http.Request) string {
	clerkJsVersion := r.URL.Query().Get("clerk_js_version")
	if clerkJsVersion == "" {
		clerkJsVersion = "latest"

		if cenv.IsStaging() {
			clerkJsVersion = "canary"
		}
	}

	return fmt.Sprintf("https://%s/npm/@clerk/clerk-js@%s/dist/clerk.browser.js", fapiHost, clerkJsVersion)
}

func renderInterstitial(w http.ResponseWriter, data templates.InterstitialPageData) apierror.Error {
	w.Header().Set("Content-Type", "text/html")

	err := templates.RenderInterstitial(w, data)
	if err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}
