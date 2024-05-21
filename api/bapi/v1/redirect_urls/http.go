package redirect_urls

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

const redirectURLID = "redirectURLID"

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, clock clockwork.Clock) *HTTP {
	return &HTTP{service: NewService(db, clock)}
}

// GET /v1/redirect_urls
func (h *HTTP) ReadAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if r.URL.Query().Get(param.Paginated.Name) == "true" {
		return h.service.ReadAllPaginated(r.Context())
	}
	return h.service.ReadAll(r.Context())
}

// GET /v1/redirect_urls/:redirectURLID
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context(), chi.URLParam(r, redirectURLID))
}

// POST /v1/redirect_urls
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// DELETE /v1/redirect_urls/:redirectURLID
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Delete(r.Context(), chi.URLParam(r, redirectURLID))
}
