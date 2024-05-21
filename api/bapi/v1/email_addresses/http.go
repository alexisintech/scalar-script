package email_addresses

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

const emailAddressID = "emailAddressID"

// HTTP is the http layer for all requests related to email addresses in the Backend API.
// It has 2 responsibilities:
// - extract any relevant information required by the service layer from the incoming request.
// - verify correctness of the incoming payload.

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// POST /v1/email_addresses
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// GET /v1/email_addresses/{emailAddressID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context(), chi.URLParam(r, emailAddressID))
}

// PATCH /v1/email_addresses/{emailAddressID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Update(r.Context(), chi.URLParam(r, emailAddressID), params)
}

// DELETE /v1/email_addresses/{emailAddressID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Delete(r.Context(), chi.URLParam(r, emailAddressID))
}
