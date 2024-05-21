package sign_in_tokens

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

type HTTP struct {
	signInTokensService *Service
}

func NewHTTP(clock clockwork.Clock, db database.Database) *HTTP {
	return &HTTP{
		signInTokensService: NewService(clock, db),
	}
}

// POST /v1/sign_in_tokens
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.signInTokensService.Create(r.Context(), params)
}

// POST /v1/sign_in_tokens/{signInTokenID}/revoke
func (h *HTTP) Revoke(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	signInTokenID := chi.URLParam(r, "signInTokenID")
	return h.signInTokensService.Revoke(r.Context(), signInTokenID)
}
