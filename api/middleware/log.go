package middleware

import (
	"database/sql"
	"net/http"
	"time"

	"clerk/pkg/cenv"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/sampling"
	"clerk/utils/log"
)

// Log injects a log.CanonicalLine in the context and also emits the log.
func Log(dbStatsSnapshot func() sql.DBStats) func(next http.Handler) http.Handler {
	dbStatsSampleRate := cenv.GetFloat64(cenv.ClerkDBStatsSampling)
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			inspectableResponseWriter := log.NewLoggableResponseWriter(w)
			logLine := log.NewCanonicalLine(r, inspectableResponseWriter)

			ctx = log.AddLogLineToContext(ctx, logLine)
			if sampling.IsIncluded(dbStatsSampleRate) {
				log.AddToLogLine(ctx, log.DBStats, dbStatsSnapshot())
			}
			log.AddToLogLine(ctx, log.MaintenanceMode, maintenance.FromContext(ctx))
			next.ServeHTTP(inspectableResponseWriter, r.WithContext(ctx))

			logLine.FinishedAt = time.Now()
			log.CanonicalLineEntry(ctx, logLine)
		}
		return http.HandlerFunc(fn)
	}
}
