package display_config

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/model/sqbmodel_extensions"
	"clerk/pkg/ctx/validator"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/params"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
	"github.com/vgarvardt/gue/v2"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, gueClient *gue.Client, clerkImagesClient *clerkimages.Client) *HTTP {
	return &HTTP{
		service: NewService(db, gueClient, clerkImagesClient),
	}
}

// GET /instances/{instanceID}/display_config
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context())
}

// PATCH /instances/{instanceID}/display_config
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	var dcSettings params.DisplayConfigSettings
	if err := json.NewDecoder(r.Body).Decode(&dcSettings); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Update(ctx, instanceID, dcSettings)
}

// GET /instances/{instanceID}/theme
func (h *HTTP) GetTheme(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.GetTheme(r.Context())
}

// PATCH /instances/{instanceID}/theme
func (h *HTTP) UpdateTheme(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	var theme model.Theme
	if err := json.NewDecoder(r.Body).Decode(&theme); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	return h.service.UpdateTheme(ctx, instanceID, &theme)
}

// POST /instances/{instanceID}/image_settings
func (h *HTTP) UpdateImageSettings(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	var imageSettings sqbmodel_extensions.DisplayConfigImageSettings
	if err := json.NewDecoder(r.Body).Decode(&imageSettings); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	validate := validator.FromContext(ctx)
	if err := validate.Struct(imageSettings); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	return h.service.UpdateImageSettings(ctx, instanceID, imageSettings)
}

// GET /instances/{instanceID}/image_settings
func (h *HTTP) GetImageSettings(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.GetImageSettings(r.Context())
}
