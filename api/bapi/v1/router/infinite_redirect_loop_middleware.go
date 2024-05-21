package router

import (
	"net/http"
	"net/url"

	"clerk/api/apierror"
)

// infiniteRedirectLoop checks whether the incoming request Referer header is the same as the requested URL
// If it does, it throws an error to stop the infinite redirect loop
func infiniteRedirectLoop() func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		referer := r.Header.Get("Referer")
		if referer == "" {
			return r, nil
		}

		refererURL, err := url.Parse(referer)
		if err != nil {
			return r, nil
		}

		samePath := refererURL.RawPath == r.URL.RawPath
		sameQueryParams := refererURL.Query().Encode() == r.URL.Query().Encode()
		if samePath && sameQueryParams {
			return nil, apierror.InfiniteRedirectLoop()
		}

		return r, nil
	}
}
