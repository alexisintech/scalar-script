package organization_memberships

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

// Form parameters used in organization related HTTP requests.
var (
	paramRole   = param.NewSingle(param.T.String, "role", nil)
	paramUserID = param.NewSingle(param.T.String, "user_id", nil)
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

// POST /v1/organizations/{organizationID}/memberships
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	err := form.Check(r.Form, param.NewList(param.NewSet(paramUserID, paramRole), param.NewSet()))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	params := &CreateMembershipParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		UserID:           *form.GetString(r.Form, paramUserID.Name),
		Role:             *form.GetString(r.Form, paramRole.Name),
		RequestingUserID: reqUser.ID,
	}
	res, err := h.service.Create(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// PATCH /v1/organizations/{organizationID}/memberships/{userID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	err := form.Check(r.Form, param.NewList(param.NewSet(paramRole), param.NewSet()))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	params := &UpdateMembershipParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		UserID:           chi.URLParam(r, "userID"),
		Role:             *form.GetString(r.Form, paramRole.Name),
		RequestingUserID: reqUser.ID,
	}
	res, err := h.service.Update(ctx, params)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// GET /v1/organizations/{organizationID}/memberships
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	err := form.CheckWithPagination(r.Form, param.NewList(param.NewSet(), param.NewSet(param.Roles, param.Paginated)))
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	members, err := h.service.List(ctx, ListMembershipsParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		RequestingUserID: reqUser.ID,
		Roles:            form.GetStringArray(r.Form, param.Roles.Name),
		Paginated:        form.GetBool(r.Form, param.Paginated.Name),
	}, paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, members, client)
}

// DELETE /v1/organizations/{organizationID}/memberships/{userID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	reqUser := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	membership, err := h.service.Delete(ctx, DeleteMembershipParams{
		OrganizationID:   chi.URLParam(r, "organizationID"),
		UserID:           chi.URLParam(r, "userID"),
		RequestingUserID: reqUser.ID,
	})
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, membership, client)
}
