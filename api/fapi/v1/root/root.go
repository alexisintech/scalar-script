package root

import (
	"net/http"

	"clerk/api/apierror"
)

// GET /
func Root(w http.ResponseWriter, _ *http.Request) (interface{}, apierror.Error) {
	w.WriteHeader(http.StatusOK)
	return nil, nil
}
