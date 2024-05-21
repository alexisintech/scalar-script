package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/activity"
	"clerk/pkg/transformers"
	"clerk/utils/log"
)

// withSessionActivity is a middleware that parses the request and generates
// the corresponding session activity.
func withSessionActivity(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()

	sessionActivity := transformers.ToSessionActivity(r)
	ctx = activity.NewContext(ctx, sessionActivity)

	log.AddToLogLine(ctx, log.Activity, &log.DeviceActivity{
		BrowserName:    sessionActivity.BrowserName.String,
		BrowserVersion: sessionActivity.BrowserVersion.String,
		DeviceType:     sessionActivity.DeviceType.String,
		City:           sessionActivity.City.String,
		Country:        sessionActivity.Country.String,
	})

	return r.WithContext(ctx), nil
}
