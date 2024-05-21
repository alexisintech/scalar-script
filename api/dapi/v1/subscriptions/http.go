package subscriptions

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps, paymentProvider billing.PaymentProvider) *HTTP {
	return &HTTP{
		service: NewService(deps, paymentProvider),
	}
}

func (h *HTTP) NewPricingCheckoutEnabled(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if !cenv.GetBool(cenv.FlagAllowNewPricingCheckout) {
		return r, apierror.ResourceNotFound()
	}
	return r, nil
}

// POST /applications/{applicationID}/checkout_subscription
func (h *HTTP) ApplicationCheckout(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	var params checkoutParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	params.resourceID = chi.URLParam(r, "applicationID")
	params.resourceType = constants.ApplicationResource
	ctx := r.Context()
	params.ownerID, params.ownerType = sdkutils.OwnerFrom(ctx)

	return h.service.Checkout(ctx, params)
}

// POST /applications/{applicationID}/complete_subscription
func (h *HTTP) ApplicationComplete(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.ApplicationComplete(r.Context(), completeParams{
		resourceID: chi.URLParam(r, "applicationID"),
	})
}

// POST /organizations/{organizationID}/checkout_subscription
func (h *HTTP) OrganizationCheckout(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	var params checkoutParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	organizationID := chi.URLParam(r, "organizationID")
	params.resourceID = organizationID
	params.resourceType = constants.OrganizationResource
	params.ownerID = organizationID
	params.ownerType = constants.OrganizationResource

	return h.service.Checkout(r.Context(), params)
}

// POST /organizations/{organizationID}/complete_subscription
func (h *HTTP) OrganizationComplete(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.OrganizationComplete(r.Context(), completeParams{
		resourceID: chi.URLParam(r, "organizationID"),
	})
}
