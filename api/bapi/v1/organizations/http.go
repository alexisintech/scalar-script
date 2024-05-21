package organizations

import (
	"net/http"
	"strconv"

	"clerk/api/apierror"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/environment"
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

// Middleware /v1/organizations/{organizationID}
func (h *HTTP) EnsureOrganizationExists(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	apiErr := h.service.EnsureOrganizationExists(r.Context(), chi.URLParam(r, "organizationID"))
	return r, apiErr
}

// Middleware /v1/organizations/{organizationID}
func (h *HTTP) EmitActiveOrganizationEventIfNeeded(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	h.service.EmitActiveOrganizationEventIfNeeded(r.Context(), chi.URLParam(r, "organizationID"))
	return r, nil
}

// Middleware /v1/organizations/{organizationID}
func (h *HTTP) CheckOrganizationsEnabled(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	env := environment.FromContext(r.Context())
	if !env.AuthConfig.IsOrganizationsEnabled() {
		return r, apierror.OrganizationNotEnabledInInstance()
	}
	return r, nil
}

// List handles requests to
// GET /v1/organizations
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	includeMembersCount, _ := strconv.ParseBool(r.URL.Query().Get("include_members_count"))
	return h.service.List(r.Context(), ListParams{
		IncludeMembersCount: includeMembersCount,
		Query:               r.URL.Query().Get("query"),
		UserIDs:             r.URL.Query()["user_id"],
		orderBy:             clerkhttp.GetOptionalQueryParam(r, "order_by"),
	}, paginationParams)
}

// POST /v1/organizations
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	return h.service.Create(r.Context(), params)
}

// Read handles requests to
// GET /v1/organizations/{organizationID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context(), chi.URLParam(r, "organizationID"))
}

// DELETE /v1/organizations/{organizationID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Delete(r.Context(), DeleteParams{
		OrganizationID: chi.URLParam(r, "organizationID"),
	})
}

// PATCH /v1/organizations/{organizationID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.OrganizationID = chi.URLParam(r, "organizationID")
	return h.service.Update(r.Context(), params)
}

// UpdateLogo handles requests to
// POST /v1/organizations/{organizationID}/logo
func (h *HTTP) UpdateLogo(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	// Allow up to 10MB files, add an extra KB for the user ID
	const tenMB = 10*1024*1024 + 1*1024

	if err := r.ParseMultipartForm(tenMB); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	uploaderUserID := r.Form.Get("uploader_user_id")

	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	if file == nil {
		return nil, apierror.RequestWithoutImage()
	}

	ctx := r.Context()
	env := environment.FromContext(ctx)
	params := organizations.UpdateLogoParams{
		OrganizationID: chi.URLParam(r, "organizationID"),
		Image:          file,
		Filename:       header.Filename,
		UploaderUserID: uploaderUserID,
	}
	return h.service.UpdateLogo(ctx, params, env.Instance)
}

func (h *HTTP) DeleteLogo(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.DeleteLogo(r.Context(), chi.URLParam(r, "organizationID"))
}

// UpdateMetadata handles requests to
// PATCH /v1/organizations/{organizationID}/metadata
func (h *HTTP) UpdateMetadata(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateMetadataParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	params.OrganizationID = chi.URLParam(r, "organizationID")

	return h.service.UpdateMetadata(r.Context(), params)
}
