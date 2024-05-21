package organization_domains

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/wrapper"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
	"clerk/utils/clerk"
	"clerk/utils/form"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
	wrapper *wrapper.Wrapper
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
		wrapper: wrapper.NewWrapper(deps),
	}
}

// POST /v1/organizations/{organizationID}/domains
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.OrgDomainName), param.NewSet()))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	createForm := CreateParams{
		Name:           *form.GetString(r.Form, param.OrgDomainName.Name),
		OrganizationID: chi.URLParam(r, "organizationID"),
	}
	response, err := h.service.Create(ctx, createForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, response, client)
}

// GET /v1/organizations/{organizationID}/domains
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	err := form.CheckWithPagination(r.Form, param.NewList(param.NewSet(), param.NewSet(param.OrgDomainVerified, param.OrgDomainEnrollmentMode)))
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	domainsPaginatedResponse, err := h.service.ListOrganizationDomains(ctx, ListOrganizationDomainsParams{
		OrganizationID:  chi.URLParam(r, "organizationID"),
		Verified:        form.GetBool(r.Form, param.OrgDomainVerified.Name),
		EnrollmentModes: form.GetStringArray(r.Form, param.OrgDomainEnrollmentMode.Name),
	}, paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, domainsPaginatedResponse, client)
}

// GET /v1/organizations/{organizationID}/domains/{domainID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	response, err := h.service.Read(ctx, chi.URLParam(r, "organizationID"), chi.URLParam(r, "domainID"))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// POST /v1/organizations/{orgID}/domains/{domainID}/prepare_affiliation_verification
func (h *HTTP) PrepareAffiliationVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.AffiliationEmailAddress), param.NewSet()))
	if err != nil {
		return nil, err
	}

	params := PrepareParams{
		AffiliationEmailAddress: *form.GetString(r.Form, param.AffiliationEmailAddress.Name),
		OrganizationID:          chi.URLParam(r, "organizationID"),
		OrganizationDomainID:    chi.URLParam(r, "domainID"),
	}
	response, err := h.service.PrepareAffiliationVerification(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, response, client)
}

// POST /v1/organizations/{orgID}/domains/{domainID}/attempt_affiliation_verification
func (h *HTTP) AttemptAffiliationVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.Code), param.NewSet()))
	if err != nil {
		return nil, err
	}

	params := AttemptParams{
		Code:                 *form.GetString(r.Form, param.Code.Name),
		OrganizationID:       chi.URLParam(r, "organizationID"),
		OrganizationDomainID: chi.URLParam(r, "domainID"),
	}
	response, err := h.service.AttemptAffiliationVerification(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, response, client)
}

// POST /v1/organizations/{orgID}/domains/{domainID}/update_enrollment_mode
func (h *HTTP) UpdateEnrollmentMode(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.OrgDomainEnrollmentMode), param.NewSet(param.OrgDomainDeletePending)))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	updateForm := UpdateEnrollmentModeParams{
		EnrollmentMode:       *form.GetString(r.Form, param.OrgDomainEnrollmentMode.Name),
		OrganizationID:       chi.URLParam(r, "organizationID"),
		OrganizationDomainID: chi.URLParam(r, "domainID"),
		DeletePending:        form.GetBool(r.Form, param.OrgDomainDeletePending.Name),
	}
	response, err := h.service.UpdateEnrollmentMode(ctx, updateForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, response, client)
}

// DELETE /v1/organizations/{orgID}/domains/{domainID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	params := DeleteParams{
		OrganizationID:       chi.URLParam(r, "organizationID"),
		OrganizationDomainID: chi.URLParam(r, "domainID"),
	}
	res, err := h.service.Delete(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// Middleware /v1/organizations/{orgID}/domains
func (h *HTTP) EnsureDomainsEnabled(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	if !env.AuthConfig.IsOrganizationDomainsEnabled() {
		return r, apierror.OrganizationDomainsNotEnabled()
	}
	return r, nil
}
