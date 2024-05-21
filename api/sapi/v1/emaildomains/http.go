package emaildomains

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	emailDomain := chi.URLParam(r, "emailDomain")
	return h.service.Read(r.Context(), emailDomain)
}

func (h *HTTP) CheckQuality(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := CheckEmailQualityParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.CheckQuality(r.Context(), params)
}

func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.EmailAddressOrDomain = chi.URLParam(r, "emailDomain")
	return h.service.Update(r.Context(), params)
}

func (h *HTTP) UpdateQuality(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Update(r.Context(), params)
}
