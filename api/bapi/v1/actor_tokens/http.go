package actor_tokens

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

// POST /v1/actor_tokens
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// POST /v1/actor_tokens/{actorTokenID}/revoke
func (h *HTTP) Revoke(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	actorTokenID := chi.URLParam(r, "actorTokenID")
	return h.service.Revoke(r.Context(), actorTokenID)
}
