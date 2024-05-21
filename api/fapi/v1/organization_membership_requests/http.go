package organization_membership_requests

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/wrapper"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/pkg/ctx/requesting_user"
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

// GET /v1/organizations/{organizationID}/membership_requests
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	err := form.CheckWithPagination(r.Form, param.NewList(param.NewSet(), param.NewSet(param.Status)))
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		OrganizationID: chi.URLParam(r, "organizationID"),
		Statuses:       form.GetStringArray(r.Form, param.Status.Name),
	}
	response, err := h.service.List(ctx, params, paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// POST /v1/organizations/{organizationID}/membership_requests/{requestID}/accept
func (h *HTTP) Accept(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	requestingUser := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	params := AcceptParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		RequestID:        chi.URLParam(r, "requestID"),
		RequestingUserID: requestingUser.ID,
	}
	response, err := h.service.Accept(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// POST /v1/organizations/{organizationID}/membership_requests/{requestID}/reject
func (h *HTTP) Reject(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	requestingUser := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	params := AcceptParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		RequestID:        chi.URLParam(r, "requestID"),
		RequestingUserID: requestingUser.ID,
	}
	response, err := h.service.Reject(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}
