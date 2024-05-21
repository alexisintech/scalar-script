package edge_events

import (
	"fmt"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/sentry"
	"clerk/utils/clerk"
	"clerk/utils/url"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/idtoken"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// Middleware for /v1/internal/edge-events
func (h *HTTP) ValidateGoogleJWT(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	// Note: gcloud pubsub emulator will not send an Authorization header

	// skip auth header check; should only apply to dev/test environments
	if cenv.IsEnabled(cenv.ClerkSkipBAPIPubSubPushAuthCheck) {
		return r, nil
	}

	ctx := r.Context()

	// Get the Cloud Pub/Sub-generated JWT in the "Authorization" header.
	token, err := url.BearerAuthHeader(r)
	if err != nil {
		return r, err
	}

	if _, err := idtoken.Validate(ctx, token, ""); err != nil {
		return r, apierror.URLNotFound()
	}

	return r, nil
}

// https://cloud.google.com/pubsub/docs/push#receive_push
type PubSubPayload struct {
	Message      pubsub.Message `json:"message"`
	Subscription string         `json:"subscription"`
}

// This is an arbitrary response schema to PubSub. Only status code matters,
// but we'll return json for API consistency and include any hints for debugging.
type Response struct {
	Status string `json:"status"`
}

// POST /v1/internal/edge-events
func (h *HTTP) HandlePubsubPush(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	var payload PubSubPayload
	if err := clerkhttp.Decode(r, &payload); err != nil {
		// GCP payload is likely malformatted
		sentry.CaptureException(ctx, fmt.Errorf("edge_events/http: failed to decode request: %w", err))
		return Response{
			Status: "failed to decode request",
		}, nil
	}

	if err := h.service.HandleMessage(ctx, payload.Message); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return Response{
		Status: "message handled",
	}, nil
}
