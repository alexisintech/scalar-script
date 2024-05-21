package engineering

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/cache"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(cache cache.Cache) *HTTP {
	return &HTTP{
		service: NewService(cache),
	}
}

func (h *HTTP) Set(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := SetParams{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	params.Key = chi.URLParam(r, paramKey)
	err := h.service.Set(r.Context(), params)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

func (h *HTTP) Get(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Get(r.Context(), chi.URLParam(r, paramKey))
}

func (h *HTTP) Exists(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Exists(r.Context(), chi.URLParam(r, paramKey))
}
