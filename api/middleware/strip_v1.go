package middleware

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// StripV1 is a middleware that will match request paths with a leading "/v1",
// strip it from the path, and continue routing through the mux.
// In this way, a server with this middleware will serve all requests from
// that have a leading "/v1" as if they had no leading "/v1".
// When wiring this middleware into a router, it is important to
// update the routers paths to omit the v1 prefix.
func StripV1(next http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var path string

			// rctx.RoutePath is only set once a route has matched to a path,
			// and is therefore only relevant in sub-paths.
			// Checking for it here allows this middleware to function when
			// mounted in the middle of an existing router chain.
			rctx := chi.RouteContext(r.Context())
			if rctx != nil && rctx.RoutePath != "" {
				path = rctx.RoutePath
			} else {
				path = r.URL.Path
			}
			var newPath string
			if path == "/v1" {
				newPath = "/"
			} else if strings.HasPrefix(path, "/v1/") {
				newPath = path[3:] // strip "/v1"
			}
			if newPath != "" {
				if rctx == nil {
					// NOTE(izaak): To my reading, and in the test cases I have,
					// the chi router preferentially respects the RoutePath over
					// the URL.Path, so we might not need this.
					// That said, the built-in chi StripSlashes middleware
					// follows this pattern: https://github.com/go-chi/chi/blob/ef31c0bff3f8061e6fba7085691e4970a3d1b7da/middleware/strip.go#L23-L28
					// And I can't think of a reason _not_ to also update r.URL.Path.
					r.URL.Path = newPath
				} else {
					rctx.RoutePath = newPath
				}
			}
			next.ServeHTTP(w, r)
		},
	)
}
