package templates

import (
	"net/http"

	"github.com/jonboulle/clockwork"

	"clerk/api/apierror"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/constants"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

// HTTP is the http layer for all requests related to templates in the server API.
// Its responsibility is to extract any relevant information required by the service layer from the incoming request.
// It's also responsible for verifying the correctness of the incoming payload.
type HTTP struct {
	db      database.Database
	service *Service
}

func NewHTTP(clock clockwork.Clock, db database.Database) *HTTP {
	return &HTTP{
		db:      db,
		service: NewService(clock, db),
	}
}

// GET /v1/templates/{template_type}
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	templateType := chi.URLParam(r, "template_type")
	if r.URL.Query().Get(param.Paginated.Name) == "true" {
		return h.service.ReadAllPaginated(r.Context(), templateType)
	}
	return h.service.ReadAll(r.Context(), templateType)
}

// GET /v1/templates/{template_type}/{slug}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	templateType := chi.URLParam(r, "template_type")
	slug := chi.URLParam(r, "slug")

	return h.service.Read(r.Context(), templateType, slug)
}

// PUT /v1/templates/{template_type}/{slug}
func (h *HTTP) Upsert(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpsertParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.TemplateType = chi.URLParam(r, "template_type")
	if params.TemplateType == string(constants.TTSMS) &&
		!cenv.IsEnabled(cenv.FlagAllowUpsertSMSCustomTemplates) {
		return nil, apierror.FeatureNotEnabled()
	}

	params.Slug = chi.URLParam(r, "slug")

	return h.service.Upsert(r.Context(), params)
}

// POST /v1/templates/{template_type}/{slug}/revert
func (h *HTTP) Revert(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	templateType := chi.URLParam(r, "template_type")
	slug := chi.URLParam(r, "slug")

	return h.service.Revert(r.Context(), templateType, slug)
}

// DELETE /v1/templates/{template_type}/{slug}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	templateType := chi.URLParam(r, "template_type")
	slug := chi.URLParam(r, "slug")

	return h.service.Delete(r.Context(), templateType, slug)
}

// POST /v1/templates/{template_type}/{slug}/preview
func (h *HTTP) Preview(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := PreviewParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.TemplateType = chi.URLParam(r, "template_type")
	params.Slug = chi.URLParam(r, "slug")

	return h.service.Preview(r.Context(), params)
}

// POST /v1/templates/{template_type}/{slug}/toggle_delivery
func (h *HTTP) ToggleDelivery(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := ToggleDeliveryParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.TemplateType = chi.URLParam(r, "template_type")
	params.Slug = chi.URLParam(r, "slug")

	return h.service.ToggleDelivery(r.Context(), params)
}
