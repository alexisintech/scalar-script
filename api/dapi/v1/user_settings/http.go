package user_settings

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	"github.com/clerk/clerk-sdk-go/v2/instancesettings"
	"github.com/go-chi/chi/v5"
	"github.com/vgarvardt/gue/v2"
)

const (
	instanceIDParam = "instanceID"
	providerIDParam = "providerID"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, gueClient *gue.Client, newSDKConfig sdkutils.ConfigConstructor) *HTTP {
	return &HTTP{
		service: NewService(db, gueClient, newSDKConfig),
	}
}

// GET /instances/{instanceID}/user_settings
func (h *HTTP) FindAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, instanceIDParam)
	return h.service.FindInstanceUserSettings(r.Context(), instanceID)
}

// UpdateSessions handles requests to
// PATCH /instances/{instanceID}/user_settings/sessions
func (h *HTTP) UpdateUserSettingsSessions(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params UpdateSessionsParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	return h.service.UpdateSessionSettings(r.Context(), params)
}

// UpdateUserSettings handles requests to
// PATCH /instances/{instanceID}/user_settings
func (h *HTTP) UpdateUserSettings(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params map[string]interface{}
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.UpdateUserSettings(r.Context(), chi.URLParam(r, instanceIDParam), params)
}

// PATCH /instances/{instanceID}/user_settings/social/{providerID}
func (h *HTTP) UpdateUserSettingsSocial(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params socialParams

	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return nil, h.service.UpdateSocial(
		r.Context(),
		chi.URLParam(r, instanceIDParam),
		chi.URLParam(r, providerIDParam),
		params,
	)
}

// PATCH /instances/{instanceID}/user_settings/restrictions
func (h *HTTP) UpdateRestrictions(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params instancesettings.UpdateRestrictionsParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, instanceIDParam)
	return h.service.UpdateRestrictions(r.Context(), instanceID, params)
}

// PATCH /instances/{instanceID}/user_settings/psu
func (h HTTP) SwitchToPSU(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.SwitchToPSU(r.Context())
}
