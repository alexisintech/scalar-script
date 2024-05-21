package organization_permissions

import (
	"encoding/json"
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
	return &HTTP{
		service: NewService(deps),
	}
}

// GET /instances/{instanceID}/organization_permissions
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		Params:  paginationParams,
		Query:   clerkhttp.GetOptionalQueryParam(r, "query"),
		OrderBy: clerkhttp.GetOptionalQueryParam(r, "order_by"),
	}

	return h.service.List(r.Context(), chi.URLParam(r, "instanceID"), params)
}

// POST /instances/{instanceID}/organization_permissions
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Create(r.Context(), chi.URLParam(r, "instanceID"), params)
}

// GET /instances/{instanceID}/organization_permissions/{orgPermissionID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	orgPermissionID := chi.URLParam(r, "orgPermissionID")

	return h.service.Read(r.Context(), instanceID, orgPermissionID)
}

// PATCH /instances/{instanceID}/organization_permissions/{orgPermissionID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	orgPermissionID := chi.URLParam(r, "orgPermissionID")

	params := UpdateParams{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Update(r.Context(), instanceID, orgPermissionID, params)
}

// DELETE /instances/{instanceID}/organization_permissions/{orgPermissionID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	orgPermissionID := chi.URLParam(r, "orgPermissionID")

	return h.service.Delete(r.Context(), instanceID, orgPermissionID)
}
