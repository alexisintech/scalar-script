package allowlist

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{service: NewService(deps)}
}

// GET /v1/allowlist_identifiers
func (h *HTTP) ReadAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	if r.URL.Query().Get(param.Paginated.Name) == "true" {
		return h.service.ReadAllPaginated(ctx)
	}
	return h.service.ReadAll(ctx)
}

// POST /v1/allowlist_identifiers
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// DELETE /v1/allowlist_identifiers/{identifierID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	identifierID := chi.URLParam(r, "identifierID")
	return h.service.Delete(r.Context(), identifierID)
}
