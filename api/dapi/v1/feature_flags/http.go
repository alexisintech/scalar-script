package feature_flags

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// GET /instances/{instanceID}/feature_flags
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Read(r.Context(), instanceID)
}
