package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"clerk/pkg/constants"
	"clerk/pkg/ctx/trace"

	"github.com/getsentry/sentry-go"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

const (
	googleCloudTraceHeader = "X-Cloud-Trace-Context"
)

// SetTraceID adds a trace token into r.Context and Sentry's current scope.
//
// NOTE: Should be added after the sentry/http middleware, since it relies on
// the Sentry Hub being populated in the context.
func SetTraceID(next http.Handler) http.Handler {
	tracingMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			traceID := getTraceID(r)
			sentry.GetHubFromContext(ctx).ConfigureScope(func(scope *sentry.Scope) {
				scope.SetTag("trace_id", traceID)
			})

			newCtx := trace.NewContext(ctx, traceID)

			// Set the trace id in the response header, for general observability
			w.Header().Set(constants.ClerkTraceID, traceID)

			next.ServeHTTP(w, r.WithContext(newCtx))
		})
	}

	return chi.Chain(
		// Inject request ID into the context
		chimw.RequestID,
		tracingMiddleware,
	).Handler(next)
}

func getTraceID(r *http.Request) string {
	// If the request has a trace id header, use it
	if r.Header.Get(constants.ClerkTraceID) != "" {
		return r.Header.Get(constants.ClerkTraceID)
	}

	// If the request has a trace id query param, use it
	traceIDEncoded := r.URL.Query().Get(constants.TraceIDQueryParam)

	if traceIDEncoded != "" {
		traceIDDecoded, err := url.QueryUnescape(traceIDEncoded)
		if err == nil {
			return traceIDDecoded
		}
	}

	// If the request has a Google Cloud trace id, use it
	traceID := strings.Split(r.Header.Get(googleCloudTraceHeader), "/")[0]

	// Fallback to generated request id from RequestID middleware
	if traceID == "" {
		traceID = chimw.GetReqID(r.Context())
	}
	return traceID
}
