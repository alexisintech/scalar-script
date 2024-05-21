package saml_connections

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	"github.com/clerk/clerk-sdk-go/v2/samlconnection"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, newSDKConfig sdkutils.ConfigConstructor) *HTTP {
	return &HTTP{
		service: NewService(db, newSDKConfig),
	}
}

// GET /instances/{instanceID}/saml_connections
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		pagination: paginationParams,
		query:      clerkhttp.GetOptionalQueryParam(r, "query"),
		orderBy:    clerkhttp.GetOptionalQueryParam(r, "order_by"),
	}

	return h.service.List(r.Context(), instanceID, params)
}

// GET /instances/{instanceID}/saml_connections/{samlConnectionID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	samlConnectionID := chi.URLParam(r, "samlConnectionID")

	return h.service.Read(r.Context(), instanceID, samlConnectionID)
}

// POST /instances/{instanceID}/saml_connections
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")

	params := &samlconnection.CreateParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Create(r.Context(), instanceID, params)
}

// PATCH /instances/{instanceID}/saml_connections/{samlConnectionID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	samlConnectionID := chi.URLParam(r, "samlConnectionID")

	params := &samlconnection.UpdateParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Update(r.Context(), instanceID, samlConnectionID, params)
}

// DELETE /instances/{instanceID}/saml_connections/{samlConnectionID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	samlConnectionID := chi.URLParam(r, "samlConnectionID")

	return h.service.Delete(r.Context(), instanceID, samlConnectionID)
}
