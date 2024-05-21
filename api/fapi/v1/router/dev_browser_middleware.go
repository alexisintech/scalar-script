package router

import (
	"net/http"
	"strings"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/jwtcontext"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/requestingdevbrowser"
	clerkmaintenance "clerk/pkg/maintenance"
	clerksentry "clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/log"
)

// on paths that typically create new sessions (i.e. a user signs in)
// there might be no DevBrowser yet
var ignoredPaths = set.New(
	"/v1/client/link",
	"/v1/client/sync",
	"/v1/client/handshake",
	"/v1/dev_browser",
	"/v1/oauth_callback",
	"/v1/verify",
	"/v1/tickets/accept",
	"/oauth/authorize",
	"/v1/saml/acs/",
	"/v1/saml/metadata/",
).Array()

func setDevBrowser(deps clerk.Deps) func(http.ResponseWriter, *http.Request) (*http.Request, apierror.Error) {
	cache := deps.Cache()
	db := deps.DB()
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		ctx := r.Context()
		env := environment.FromContext(ctx)
		clientType := client_type.FromContext(ctx)
		jwt := jwtcontext.FromContext(ctx)

		// DevBrowsers are only used in development instances in standard browser clients.
		// Nothing to set on the current request, if we're in production or if it's a native client.
		if env.Instance.IsProduction() || clientType.IsNative() {
			if jwt != nil && jwt["dev"] != nil {
				devClaim, devClaimIsString := jwt["dev"].(string)

				if devClaimIsString && devClaim != "" {
					log.Info(ctx, "Found invalid dev browser usage. InstanceID=%s  IsProduction=%t IsNativeClientType=%t", env.Instance.ID, env.Instance.IsProduction(), clientType.IsNative())
				}
			}

			return r.WithContext(ctx), nil
		}

		// Check if DevBrowser is missing from the client JWT (dev claim) or the client JWT is missing completely
		if jwt == nil || jwt["dev"] == nil {
			for _, path := range ignoredPaths {
				if strings.HasPrefix(r.URL.Path, path) {
					return r.WithContext(ctx), nil
				}
			}

			// All other paths require a DevBrowser, so we need to raise a 4XX error.
			return r.WithContext(ctx), apierror.DevBrowserUnauthenticated()
		}

		// At this point we expect a DevBrowser. Load it from the DB.
		devBrowserID, _ := jwt["dev"].(string)

		log.AddToLogLine(ctx, log.DevBrowserID, devBrowserID)

		var dvb *model.DevBrowser
		if maintenance.FromContext(ctx) {
			var devBrowser model.DevBrowser
			if err := cache.Get(ctx, clerkmaintenance.DevBrowserKey(devBrowserID, env.Instance.ID), &devBrowser); err != nil {
				clerksentry.CaptureException(ctx, err)
			} else if devBrowser.DevBrowser != nil {
				dvb = &devBrowser
			}
		}
		if dvb == nil {
			var err error
			dvb, err = repository.NewDevBrowser().QueryByIDAndInstance(ctx, db, devBrowserID, env.Instance.ID)
			if err != nil {
				return r.WithContext(ctx), apierror.Unexpected(err)
			}
		}

		if dvb != nil {
			ctx = requestingdevbrowser.NewContext(ctx, dvb)
		} else if r.URL.Path != "/v1/client/handshake" {
			// Since the JWT is signed, this should never throw an error
			// unless we start deleting DevBrowsers
			return r.WithContext(ctx), apierror.DevBrowserUnauthenticated()
		}

		return r.WithContext(ctx), nil
	}
}
