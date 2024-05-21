package account_portal

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/sdk"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps, newSDKConfig sdk.ConfigConstructor) *HTTP {
	return &HTTP{
		service: NewService(deps, newSDKConfig),
	}
}

// GET /instances/{instanceID}/account_portal
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Read(r.Context(), instanceID)
}

// PATCH /instances/{instanceID}/account_portal
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")

	params := &updateParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Update(r.Context(), instanceID, params)
}
