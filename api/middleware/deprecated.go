package middleware

import (
	"net/http"

	"clerk/utils/log"
)

// Deprecated is meant to be added to deprecated routes, to provide a way to
// easy way to identify access to such routes, via logging.
func Deprecated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.String()
		log.Info(r.Context(), "Found deprecated access: %s", url)
		next.ServeHTTP(w, r)
	})
}
