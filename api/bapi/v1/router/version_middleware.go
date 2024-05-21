package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/sdkversion"
	"clerk/utils/log"
)

const (
	clerkSDKVersionHeader = "X-Clerk-SDK"
)

// logClerkSDKVersion checks whether the incoming request contains the X-Clerk-SDK header.
// If it does, it adds it to the canonical log line so that it can be logged along with the request.
func logClerkSDKVersion(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	version := readVersion(r)
	ctx = sdkversion.NewContext(ctx, sdkversion.SDKVersion(version))
	log.AddToLogLine(ctx, log.ClerkSDKVersion, version)

	return r.WithContext(ctx), nil
}

func readVersion(r *http.Request) string {
	version := r.Header.Get(clerkSDKVersionHeader)
	if version != "" {
		return version
	}

	return "no_version"
}
