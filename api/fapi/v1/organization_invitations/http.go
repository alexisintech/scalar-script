package organization_invitations

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/wrapper"
	"clerk/api/serialize"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/ctxkeys"
	"clerk/utils/clerk"
	"clerk/utils/form"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

var (
	paramEmailAddress = param.NewSingle(param.T.String, "email_address", &param.Modifiers{
		MultiAllowed: true,
	})
	paramRole = param.NewSingle(param.T.String, "role", nil)
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

// POST /v1/organizations/{organizationID}/invitations
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	response, err := h.handleInvitationCreation(r)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response[0], client)
}

// POST /v1/organizations/{organizationID}/invitations/bulk
func (h *HTTP) CreateBulk(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	response, err := h.handleInvitationCreation(r)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

func (h *HTTP) handleInvitationCreation(r *http.Request) ([]*serialize.OrganizationInvitationResponse, apierror.Error) {
	err := form.Check(r.Form, param.NewList(param.NewSet(paramEmailAddress, paramRole), param.NewSet()))
	if err != nil {
		return nil, err
	}

	organizationID := chi.URLParam(r, "organizationID")
	createForm := CreateInvitationForm{
		OrganizationID: organizationID,
		EmailAddresses: form.GetStringArray(r.Form, paramEmailAddress.Name),
		Role:           *form.GetString(r.Form, paramRole.Name),
	}

	return h.service.Create(r.Context(), createForm)
}

// GET /v1/organizations/{organizationID}/invitations
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	err := form.CheckWithPagination(r.Form, param.NewList(param.NewSet(), param.NewSet(param.Status)))
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		RequestingUserID: reqUser.ID,
		Statuses:         form.GetStringArray(r.Form, param.Status.Name),
	}
	invitations, err := h.service.List(ctx, params, paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, invitations, client)
}

// Deprecated: Use 'List' instead with status 'pending'
// GET /v1/organizations/{organizationID}/invitations/pending
func (h *HTTP) ListPending(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	err := form.CheckWithPagination(r.Form, param.NewList(param.NewSet(), param.NewSet(param.Paginated)))
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	invitations, err := h.service.ListPendingInvitations(ctx, ListPendingInvitationsParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		RequestingUserID: reqUser.ID,
		Paginated:        form.GetBool(r.Form, param.Paginated.Name),
	}, paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, invitations, client)
}

// POST /v1/organizations/{organizationID}/invitations/{invitationID}/revoke
func (h *HTTP) Revoke(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	invitation, err := h.service.RevokeInvitation(ctx, RevokeInvitationParams{
		RevokeInvitationParams: organizations.RevokeInvitationParams{
			OrganizationID:   chi.URLParam(r, "organizationID"),
			InvitationID:     chi.URLParam(r, "invitationID"),
			RequestingUserID: reqUser.ID,
		},
	})
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, invitation, client)
}
