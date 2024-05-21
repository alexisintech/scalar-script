package templates

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/template"
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

// GET /instances/{instanceID}/templates/{template_type}
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.List(r.Context(), instanceID, &template.ListParams{
		TemplateType: sdk.TemplateType(chi.URLParam(r, "template_type")),
	})
}

// GET /instances/{instanceID}/templates/{template_type}/{slug}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	params := &template.GetParams{
		TemplateType: sdk.TemplateType(chi.URLParam(r, "template_type")),
		Slug:         chi.URLParam(r, "slug"),
	}
	return h.service.Read(r.Context(), instanceID, params)
}

// PUT /instances/{instanceID}/templates/{template_type}/{slug}
func (h *HTTP) Upsert(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params template.UpdateParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	params.TemplateType = sdk.TemplateType(chi.URLParam(r, "template_type"))
	params.Slug = chi.URLParam(r, "slug")
	return h.service.Upsert(r.Context(), instanceID, &params)
}

// POST /instances/{instanceID}/templates/{template_type}/{slug}/revert
func (h *HTTP) Revert(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	params := &template.RevertParams{
		TemplateType: sdk.TemplateType(chi.URLParam(r, "template_type")),
		Slug:         chi.URLParam(r, "slug"),
	}
	return h.service.Revert(r.Context(), instanceID, params)
}

// POST /instances/{instanceID}/templates/{template_type}/{slug}/preview
func (h *HTTP) Preview(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params template.PreviewParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	params.TemplateType = sdk.TemplateType(chi.URLParam(r, "template_type"))
	params.Slug = chi.URLParam(r, "slug")
	return h.service.Preview(r.Context(), instanceID, &params)
}

// DELETE /instances/{instanceID}/templates/{template_type}/{slug}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	params := &template.DeleteParams{
		TemplateType: sdk.TemplateType(chi.URLParam(r, "template_type")),
		Slug:         chi.URLParam(r, "slug"),
	}
	return h.service.Delete(r.Context(), instanceID, params)
}

// POST /instances/{instanceID}/templates/{template_type}/{slug}/toggle_delivery
func (h *HTTP) ToggleDelivery(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params template.ToggleDeliveryParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	params.TemplateType = sdk.TemplateType(chi.URLParam(r, "template_type"))
	params.Slug = chi.URLParam(r, "slug")
	return h.service.ToggleDelivery(r.Context(), instanceID, &params)
}
