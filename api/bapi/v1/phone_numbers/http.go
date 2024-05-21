package phone_numbers

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

const phoneNumberID = "phoneNumberID"

// HTTP is the http layer for all requests related to phone numbers in server API.
// Its responsibility is to extract any relevant information required by the service layer from the incoming request.
// It's also responsible for verifying the correctness of the incoming payload.
type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// POST /v1/phone_numbers
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// GET /v1/phone_numbers/{phoneNumberID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context(), chi.URLParam(r, phoneNumberID))
}

// PATCH /v1/phone_numbers/{phoneNumberID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Update(r.Context(), chi.URLParam(r, phoneNumberID), params)
}

// DELETE /v1/phone_numbers/{phoneNumberID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Delete(r.Context(), chi.URLParam(r, phoneNumberID))
}
