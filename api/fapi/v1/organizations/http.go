package organizations

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/wrapper"
	"clerk/api/shared/images"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/ctxkeys"
	"clerk/utils/clerk"
	"clerk/utils/form"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

// Form parameters used in organization related HTTP requests.
var (
	paramName = param.NewSingle(param.T.String, "name", nil)
	paramSlug = param.NewSingle(param.T.String, "slug", nil)
)

// HTTP handles HTTP requests related to organizations.
type HTTP struct {
	service *Service
	wrapper *wrapper.Wrapper
}

// NewHTTP returns a new HTTP object.
func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
		wrapper: wrapper.NewWrapper(deps),
	}
}

// Create handles requests to POST /v1/organizations
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	err := form.Check(r.Form, param.NewList(param.NewSet(paramName), param.NewSet(paramSlug)))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	params := &CreateParams{
		Name:                      *form.GetString(r.Form, paramName.Name),
		Slug:                      form.GetString(r.Form, paramSlug.Name),
		InstanceID:                env.Instance.ID,
		CreatedBy:                 user.ID,
		CreateOrganizationEnabled: user.CreateOrganizationEnabled,
	}
	org, err := h.service.Create(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, org, client)
}

// Middleware /v1/organizations/{organizationID}
func (h *HTTP) EnsureOrganizationExists(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	organizationID := chi.URLParam(r, "organizationID")
	if err := h.service.EnsureOrganizationExists(r.Context(), organizationID); err != nil {
		return r, err
	}
	return r, nil
}

// Middleware /v1/organizations/{organizationID}/membership_requests
func (h *HTTP) EnsureMembersManagePermission(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	user := requesting_user.FromContext(ctx)
	organizationID := chi.URLParam(r, "organizationID")

	if err := h.service.EnsureMembersManagePermission(ctx, organizationID, user.ID); err != nil {
		return r, err
	}
	return r, nil
}

// Middleware /v1/organizations/{organizationID}
func (h *HTTP) EmitActiveOrganizationEventIfNeeded(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	organizationID := chi.URLParam(r, "organizationID")
	h.service.EmitActiveOrganizationEventIfNeeded(r.Context(), organizationID)
	return r, nil
}

// GET /v1/organizations/{organizationID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	res, err := h.service.Read(ctx, chi.URLParam(r, "organizationID"), user.ID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// Update handles requests to
// PATCH /v1/organizations/{organizationID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	err := form.Check(r.Form, param.NewList(param.NewSet(), param.NewSet(paramName, paramSlug)))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	params := UpdateParams{
		Name:             form.GetString(r.Form, paramName.Name),
		Slug:             form.GetString(r.Form, paramSlug.Name),
		OrganizationID:   chi.URLParam(r, "organizationID"),
		RequestingUserID: reqUser.ID,
	}
	res, err := h.service.Update(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// DELETE /v1/organizations/{organizationID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	params := DeleteParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		RequestingUserID: reqUser.ID,
	}
	res, err := h.service.Delete(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// UpdateLogo handles requests to
// PUT /v1/organizations/{organizationID}/logo
func (h *HTTP) UpdateLogo(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	file, apiErr := images.ReadFileOrBase64(r)
	if apiErr != nil {
		return nil, apiErr
	}

	params := organizations.UpdateLogoParams{
		OrganizationID: chi.URLParam(r, "organizationID"),
		UploaderUserID: user.ID,
		Image:          file,
	}
	res, apiErr := h.service.UpdateLogo(ctx, params, env.Instance)
	if apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// DELETE /v1/organizations/{organizationID}/logo
func (h *HTTP) DeleteLogo(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	reqUser := requesting_user.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	params := DeleteLogoParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		RequestingUserID: reqUser.ID,
	}
	res, err := h.service.DeleteLogo(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, res, client)
}

func (h *HTTP) CheckOrganizationsEnabled(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	env := environment.FromContext(r.Context())
	if !env.AuthConfig.IsOrganizationsEnabled() {
		return r, apierror.OrganizationNotEnabledInInstance()
	}
	return r, nil
}

// GET /v1/organizations/{organizationID}/roles
func (h *HTTP) ListOrganizationRoles(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	err := form.CheckWithPagination(r.Form, param.NewEmptyList())
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	rolesPaginatedResponse, err := h.service.ListOrganizationRoles(ctx, chi.URLParam(r, "organizationID"), paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, rolesPaginatedResponse, client)
}
