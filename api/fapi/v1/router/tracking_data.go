package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/tracking"
	trackingctx "clerk/pkg/ctx/tracking"
)

func parseTrackingData(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()

	ctx = trackingctx.NewContext(ctx, tracking.NewDataFromRequest(r))

	return r.WithContext(ctx), nil
}
