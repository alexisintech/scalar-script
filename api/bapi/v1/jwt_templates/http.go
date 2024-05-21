package jwt_templates

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

const templateID = "templateID"

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, gueClient *gue.Client, clock clockwork.Clock) *HTTP {
	return &HTTP{service: NewService(db, gueClient, clock)}
}

// GET /v1/jwt_templates
func (h *HTTP) ReadAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if r.URL.Query().Get(param.Paginated.Name) == "true" {
		return h.service.ReadAllPaginated(r.Context())
	}
	return h.service.ReadAll(r.Context())
}

// GET /v1/jwt_templates/:templateID
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context(), chi.URLParam(r, templateID))
}

// POST /v1/jwt_templates
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateUpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// PATCH /v1/jwt_templates/:templateID
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateUpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Update(r.Context(), chi.URLParam(r, templateID), params)
}

// DELETE /v1/jwt_templates/:templateID
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Delete(r.Context(), chi.URLParam(r, templateID))
}
