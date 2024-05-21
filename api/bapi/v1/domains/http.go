package domains

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/bapi/v1/externalapp"
	"clerk/api/bapi/v1/internalapi"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(
	deps clerk.Deps,
	externalAppClient *externalapp.Client,
	internalClient *internalapi.Client,
) *HTTP {
	return &HTTP{
		service: NewService(deps, externalAppClient, internalClient),
	}
}

// POST /v1/domains
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := CreateParams{
		authorization: r.Header.Get("authorization"),
	}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// GET /v1/domains
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.List(r.Context())
}

// PATCH /v1/domains/{domainID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{
		authorization: r.Header.Get("authorization"),
	}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	domainID := chi.URLParam(r, "domainID")
	return h.service.Update(r.Context(), domainID, params)
}

// DELETE /v1/domains/{domainID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.Delete(r.Context(), chi.URLParam(r, "domainID"))
}
