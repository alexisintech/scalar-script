package jwt_templates

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	"github.com/clerk/clerk-sdk-go/v2/jwttemplate"
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

// GET /instances/{instanceID}/jwt_templates
func (h *HTTP) ReadAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.ReadAll(r.Context(), instanceID)
}

// POST /instances/{instanceID}/jwt_templates
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params jwttemplate.CreateParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Create(r.Context(), instanceID, &params)
}

// GET /instances/{instanceID}/jwt_templates/{templateID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	templateID := chi.URLParam(r, "templateID")
	return h.service.Read(r.Context(), instanceID, templateID)
}

// PATCH /instances/{instanceID}/jwt_templates/{templateID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params jwttemplate.UpdateParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	templateID := chi.URLParam(r, "templateID")
	return h.service.Update(r.Context(), instanceID, templateID, &params)
}

// DELETE /instances/{instanceID}/jwt_templates/{templateID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	templateID := chi.URLParam(r, "templateID")
	return h.service.Delete(r.Context(), instanceID, templateID)
}
