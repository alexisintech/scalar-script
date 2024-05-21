package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/client_type"
)

func setClientType(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()

	isBrowser := r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" || r.Header.Get("Cookie") != "" || r.URL.Query().Get(constants.DevSessionQueryParam) != "" || r.URL.Query().Get(constants.DevBrowserQueryParam) != ""

	const isNativeParam = "_is_native"

	// TODO: Refactor `_is_native` parameter. Client type should be set to Native
	// for all non-standard browser flows including native applications, hybrid applications
	// using Capacitor.js or Cordova, browser extensions, etc...
	if r.URL.Query().Get(isNativeParam) == "" && isBrowser {
		ctx = client_type.NewContext(ctx, client_type.Browser)
	} else {
		ctx = client_type.NewContext(ctx, client_type.Native)
	}

	// Remove the query parameter from the form as we're using strict parameter validation in our endpoints
	delete(r.Form, isNativeParam)

	return r.WithContext(ctx), nil
}
