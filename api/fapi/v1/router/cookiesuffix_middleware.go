package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/cookiessuffix"
)

func setCookiesSuffix(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	const useSuffixedCookies = "suffixed_cookies"

	ctx := cookiessuffix.NewContext(r.Context(), r.URL.Query().Get(useSuffixedCookies) == "true")
	// Remove the query parameter from the form as we're using strict parameter validation in our endpoints
	delete(r.Form, useSuffixedCookies)

	return r.WithContext(ctx), nil
}
