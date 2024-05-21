package saml_connections

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{service: NewService(deps)}
}

// POST /v1/saml_connections
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// PATCH /v1/saml_connections/{samlConnectionID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Update(r.Context(), chi.URLParam(r, "samlConnectionID"), params)
}

// GET /v1/saml_connections
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		pagination: paginationParams,
		query:      clerkhttp.GetOptionalQueryParam(r, "query"),
		orderBy:    clerkhttp.GetOptionalQueryParam(r, "order_by"),
	}

	return h.service.List(r.Context(), params)
}

// GET /v1/saml_connections/{samlConnectionID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context(), chi.URLParam(r, "samlConnectionID"))
}

// DELETE /v1/saml_connections/{samlConnectionID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Delete(r.Context(), chi.URLParam(r, "samlConnectionID"))
}
