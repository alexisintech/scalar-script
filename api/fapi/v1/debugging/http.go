package debugging

import (
	"net/http"

	"clerk/api/apierror"
)

type HTTP struct{}

func NewHTTP() *HTTP {
	return &HTTP{}
}

func (h *HTTP) ClearSiteData(w http.ResponseWriter, _ *http.Request) (interface{}, apierror.Error) {
	// Because of lack of Chrome to support the 'executionContexts' directive, we are not able to use the
	// wildcard value in this case. So we have to list all the other directives separately.
	w.Header().Add("Clear-Site-Data", `"cache", "cookies", "storage"`)
	w.WriteHeader(http.StatusOK)

	return nil, nil
}
