package organizationsettings

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	"github.com/clerk/clerk-sdk-go/v2/instancesettings"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, sdkConfigConstructor sdkutils.ConfigConstructor) *HTTP {
	return &HTTP{
		service: NewService(db, sdkConfigConstructor),
	}
}

// GET /instances/{instanceID}/organization_settings
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context(), chi.URLParam(r, "instanceID"))
}

// PATCH /instances/{instanceID}/organization_settings
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params instancesettings.UpdateOrganizationSettingsParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	return h.service.Update(r.Context(), chi.URLParam(r, "instanceID"), params)
}
