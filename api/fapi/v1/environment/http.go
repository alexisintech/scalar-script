package environment

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/utils/database"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

func (h *HTTP) SetEnvFromDomain(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	newCtx, err := h.service.SetEnvFromDomain(r.Context())
	if err != nil {
		return nil, err
	}
	return r.WithContext(newCtx), err
}

// GET /v1/environment
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context())
}

// PATCH /v1/environment
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	origin := r.Header.Get("Origin")

	if origin == "" {
		return nil, apierror.OriginHeaderMissing()
	}

	return h.service.Update(r.Context(), origin)
}
