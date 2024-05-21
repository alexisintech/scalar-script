package instances

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/bapi/v1/domains"
	"clerk/api/bapi/v1/externalapp"
	"clerk/api/bapi/v1/internalapi"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"
)

type HTTP struct {
	service       *Service
	domainService *domains.Service
}

func NewHTTP(deps clerk.Deps, externalAppClient *externalapp.Client, internalClient *internalapi.Client) *HTTP {
	return &HTTP{
		service:       NewService(deps),
		domainService: domains.NewService(deps, externalAppClient, internalClient),
	}
}

// PATCH /v1/instance
func (h *HTTP) Update(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateInstanceParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	err := h.service.Update(r.Context(), params)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// PATCH /v1/instance/restrictions
func (h *HTTP) UpdateRestrictions(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateRestrictionsParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.UpdateRestrictions(r.Context(), params)
}

// POST /v1/public/demo_instance
func (h *HTTP) CreateDemoInstance(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	response, err := h.service.CreateDemoInstance(r.Context())
	if err != nil {
		return nil, err
	}

	// this endpoint is going to be hit from localhosts and other unknown
	// origins
	w.Header().Add("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusCreated)

	return response, nil
}

// PATCH /v1/instance/organization_settings
func (h *HTTP) UpdateOrganizationSettings(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateOrganizationSettingsParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.UpdateOrganizationSettings(r.Context(), params)
}

// POST /v1/instance/change_domain
func (h *HTTP) UpdateHomeURL(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateHomeURLParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	ctx := r.Context()

	if err := h.service.UpdateHomeURL(ctx, params); err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusAccepted)
	return nil, nil
}
