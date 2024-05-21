package middleware

import (
	"net/http"

	"clerk/pkg/cenv"
	"clerk/pkg/ctx/environment"
)

// EnsureInstanceHasAccess responds with a 403 if the provided environment variable doesn't
// contain the current instance ID.
func EnsureInstanceHasAccess(featureFlag string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			env := environment.FromContext(r.Context())
			if !cenv.ResourceHasAccess(featureFlag, env.Instance.ID) {
				w.WriteHeader(http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
