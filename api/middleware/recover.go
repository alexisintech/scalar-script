package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
)

// Recover converts panics to an HTTP 500 response instead.
func Recover(next http.Handler) http.Handler {
	// nolint:errchkjson
	response, _ := json.Marshal(struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}{
		Type: "internal_clerk_error",
		Message: "There was an internal error on Clerk's servers. " +
			"We've been notified and are working on fixing it.",
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("\nPANIC: %v\n", r)
				debug.PrintStack()

				w.WriteHeader(http.StatusInternalServerError)

				_, _ = w.Write(response)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
