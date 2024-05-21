package environment

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

func (h *HTTP) LoadEnvFromInstance(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	newCtx, apiErr := h.service.LoadEnvFromInstance(r.Context(), instanceID)
	return r.WithContext(newCtx), apiErr
}
