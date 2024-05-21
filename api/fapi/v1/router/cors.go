package router

import (
	"net/http"

	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/versions"

	"github.com/go-chi/cors"
)

// NOTE: In staging and production, we also set CORS headers via Cloudflare
// Transform Rules. Therefore, any changes to this middleware must also be
// replicated in those rules.
func corsHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		env := environment.FromContext(ctx)

		exposedHeaders := []string{"Authorization", "x-country"}

		if env.Instance.IsDevelopmentOrStaging() {
			clerkJSVersion := clerkjs_version.FromContext(ctx)
			if versions.IsBefore(clerkJSVersion, "5.0.0", true) {
				exposedHeaders = append(exposedHeaders, constants.LegacyDevBrowserHeaders)
			} else {
				exposedHeaders = append(exposedHeaders, constants.DevBrowserHeader)
			}
		}

		c := cors.New(cors.Options{
			AllowOriginFunc: func(_ *http.Request, origin string) bool {
				return true
			},
			AllowCredentials: true,
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization"},
			ExposedHeaders:   exposedHeaders,
			MaxAge:           300, // Maximum value not ignored by any of major browsers
		})

		c.Handler(next).ServeHTTP(w, r)
	})
}
