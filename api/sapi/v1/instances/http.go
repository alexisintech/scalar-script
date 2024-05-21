package instances

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, gueClient *gue.Client) *HTTP {
	return &HTTP{
		service: NewService(db, gueClient),
	}
}

func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.Read(r.Context())
}

func (h *HTTP) UpdateOrganizationSettings(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := UpdateOrganizationSettingsParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.UpdateOrganizationSettings(r.Context(), params)
}

func (h *HTTP) UpdateSMSSettings(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := UpdateSMSSettingsParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.UpdateSMSSettings(r.Context(), params)
}

func (h *HTTP) UpdateUserLimits(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := UpdateUserLimitsParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.UpdateUserLimits(r.Context(), params)
}

func (h *HTTP) PurgeCache(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.PurgeCache(r.Context())
}
