package billing

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/wrapper"
	"clerk/model"
	"clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
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

func NewHTTP(deps clerk.Deps, billingConnector billing.Connector, paymentProvider billing.PaymentProvider) *HTTP {
	return &HTTP{
		service: NewService(deps, billingConnector, paymentProvider),
		wrapper: wrapper.NewWrapper(deps),
	}
}

// Middleware
// /v1/me/billing
// /v1/organizations/{organizationID}/billing
func (h *HTTP) EnsureBillingAccountConnected(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	env := environment.FromContext(r.Context())
	if !env.Instance.ExternalBillingAccountID.Valid {
		return r, apierror.NoBillingAccountConnectedToInstance()
	}
	return r, nil
}

// GET /v1/me/available_plans
func (h *HTTP) GetAvailablePlansForUser(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	res, err := h.service.GetAvailablePlansForCustomerType(r.Context(), constants.BillingUserType)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// GET /v1/organizations/{organizationID}/available_plans
func (h *HTTP) GetAvailablePlansForOrganization(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	res, err := h.service.GetAvailablePlansForCustomerType(ctx, constants.BillingOrganizationType)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// POST /v1/me/billing/start_portal_session
func (h *HTTP) StartPortalSessionForUser(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	requestingUser := requesting_user.FromContext(r.Context())
	return h.startPortalSession(r, requestingUser.ID)
}

// POST /v1/organizations/{organizationID}/billing/start_portal_session
func (h *HTTP) StartPortalSessionForOrganization(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.startPortalSession(r, chi.URLParam(r, "organizationID"))
}

func (h *HTTP) startPortalSession(r *http.Request, resourceID string) (any, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	reqParams := param.NewSet(param.ReturnURL)
	optParams := param.NewSet()

	paramList := param.NewList(reqParams, optParams)
	apiErr := form.Check(r.Form, paramList)
	if apiErr != nil {
		return nil, apiErr
	}

	createParams := StartPortalSessionParams{
		ResourceID: resourceID,
		ReturnURL:  *form.GetString(r.Form, param.ReturnURL.Name),
	}
	portalSession, apiErr := h.service.StartPortalSession(ctx, createParams)
	if apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, client)
	}
	return h.wrapper.WrapResponse(ctx, portalSession, client)
}

// GET /v1/me/billing/current
func (h *HTTP) GetCurrentForUser(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	current, apiErr := h.service.GetCurrentForUser(ctx)
	if apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, client)
	}
	return h.wrapper.WrapResponse(ctx, current, client)
}

// GET /v1/organizations/{organizationID}/billing/current
func (h *HTTP) GetCurrentForOrganization(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	orgID := chi.URLParam(r, "organizationID")
	current, apiErr := h.service.GetCurrentForOrganization(ctx, orgID)
	if apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, client)
	}
	return h.wrapper.WrapResponse(ctx, current, client)
}

// POST /v1/me/billing/change_plan
func (h *HTTP) ChangePlanForUser(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	params, apiErr := h.resolveChangePlanParams(r)
	if apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, client)
	}

	userParams := ChangePlanForUserParams{changePlanParams: *params}

	res, apiErr := h.service.ChangePlanForUser(ctx, userParams)
	if apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

// POST /v1/organizations/{organizationID}/billing/change_plan
func (h *HTTP) ChangePlanForOrganization(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	params, apiErr := h.resolveChangePlanParams(r)
	if apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, client)
	}

	orgParams := ChangePlanForOrganizationParams{changePlanParams: *params}
	orgParams.OrganizationID = chi.URLParam(r, "organizationID")

	res, apiErr := h.service.ChangePlanForOrganization(ctx, orgParams)
	if apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, client)
	}
	return h.wrapper.WrapResponse(ctx, res, client)
}

func (h *HTTP) resolveChangePlanParams(r *http.Request) (*changePlanParams, apierror.Error) {
	reqParams := param.NewSet(param.PlanKey, param.SuccessReturnURL)
	optParams := param.NewSet(param.CancelReturnURL)

	if err := form.Check(r.Form, param.NewList(reqParams, optParams)); err != nil {
		return nil, err
	}

	return &changePlanParams{
		PlanKey:          r.Form.Get(param.PlanKey.Name),
		SuccessReturnURL: r.Form.Get(param.SuccessReturnURL.Name),
		CancelReturnURL:  r.Form.Get(param.CancelReturnURL.Name),
	}, nil
}

func (h *HTTP) ChangePlanCallback(w http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()

	params := ChangePlanCallbackParams{
		RedirectURL:       r.URL.Query().Get("redirect_url"),
		CheckoutSessionID: r.URL.Query().Get("checkout_session_id"),
		Status:            r.URL.Query().Get("status"),
	}

	if apiErr := h.service.ChangePlanCallback(ctx, params); apiErr != nil {
		return nil, h.wrapper.WrapError(ctx, apiErr, nil)
	}

	http.Redirect(w, r, params.RedirectURL, http.StatusSeeOther)
	return nil, nil
}
