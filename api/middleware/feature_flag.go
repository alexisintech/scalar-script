package middleware

import (
	"net/http"

	"clerk/pkg/cenv"
)

// EnsureFeatureEnabled responds with a 404 if the environment variable denoted
// by featureFlag is not set to a truthy value.
func EnsureFeatureEnabled(featureFlag string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cenv.IsEnabled(featureFlag) {
				w.WriteHeader(http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
