package sentryenv

import (
	"context"

	"github.com/getsentry/sentry-go"

	"clerk/model"
)

// Enriches Sentry's current scope with request-specific data, to aid in
// debugging.
//
// See https://docs.sentry.io/platforms/go/enriching-events/scopes/.
func EnrichScope(ctx context.Context, env *model.Env) {
	hub := sentry.GetHubFromContext(ctx)
	if hub == nil {
		return
	}

	hub.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTags(map[string]string{
			"application": env.Application.Name,
			"instance_id": env.Instance.ID,
			"domain":      env.Domain.Name,
		})
	})
}
