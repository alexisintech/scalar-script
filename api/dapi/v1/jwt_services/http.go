package jwt_services

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/params"
	"clerk/utils/database"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

// GET /instances/{instanceID}/jwt_services
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context())
}

// PATCH /instances/{instanceID}/jwt_services
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	updateForm := params.UpdateJWTServicesForm{}
	if err := json.NewDecoder(r.Body).Decode(&updateForm); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Update(ctx, &updateForm)
}
