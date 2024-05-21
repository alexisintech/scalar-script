package router

import (
	"net/http"

	"clerk/api/apierror"
)

// robotsNoIndexMiddleware attaches the X-Robots-Tag to every FAPI response,
// in order to provide a hint to web crawlers that they should not index the page
// and include it or its links in search results.
// For details on the header semantics and behavior see:
// - https://developers.google.com/search/docs/crawling-indexing/robots-meta-tag#xrobotstag
// - https://en.wikipedia.org/wiki/Robots.txt#Meta_tags_and_headers
// The "noindex, nofollow" was selected to prevent links to be followed as well.
func robotsNoIndexMiddleware(w http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	w.Header().Add("X-Robots-Tag", "noindex, nofollow")
	return r, nil
}
