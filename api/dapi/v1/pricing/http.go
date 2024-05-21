package pricing

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"clerk/api/apierror"
	shorigin "clerk/api/shared/origin"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/webhook"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps, paymentProvider billing.PaymentProvider) *HTTP {
	return &HTTP{service: NewService(deps, paymentProvider)}
}

// Middleware /instances/{instanceID}
func (h *HTTP) RefreshGracePeriodFeaturesAfterUpdate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		next.ServeHTTP(w, r)
		if !clerkhttp.IsMutationMethod(r.Method) {
			return
		}

		h.service.RefreshGracePeriodFeaturesAfterUpdate(ctx, chi.URLParam(r, "instanceID"))
	})
}

func getReturnURL(r *http.Request) *url.URL {
	var params struct {
		ReturnURL string `json:"returnUrl"`
	}

	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return &url.URL{}
	}

	u, err := url.Parse(params.ReturnURL)
	if err != nil {
		return &url.URL{}
	}

	return u
}

// POST /applications/{applicationID}/checkout/{planID}/session
func (h *HTTP) OldCheckoutSessionRedirect(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CheckoutSessionRedirectParams{
		Plans:         []string{chi.URLParam(r, "planID")},
		ApplicationID: chi.URLParam(r, "applicationID"),
		SessionID:     getSessionIDFromRequest(r),
		Origin:        r.Header.Get("Origin"),
		ReturnURL:     getReturnURL(r).String(),
	}

	if !shorigin.ValidateDashboardOrigin(params.Origin) {
		return nil, apierror.InvalidOriginHeader()
	}

	return h.service.CreateAppCheckoutSession(r.Context(), params)
}

// POST /applications/{applicationID}/checkout/session
func (h *HTTP) CheckoutSessionRedirect(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params CheckoutSessionRedirectParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	params.ApplicationID = chi.URLParam(r, "applicationID")
	params.OwnerID, params.OwnerType = sdkutils.OwnerFrom(r.Context())
	params.SessionID = getSessionIDFromRequest(r)
	params.Origin = r.Header.Get("Origin")
	if !shorigin.ValidateDashboardOrigin(params.Origin) {
		return nil, apierror.InvalidOriginHeader()
	}

	return h.service.CreateAppCheckoutSession(r.Context(), params)
}

// POST /applications/{applicationID}/customer_portal_session
func (h *HTTP) ApplicationCustomerPortalRedirect(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	appID := chi.URLParam(r, "applicationID")
	sessionID := getSessionIDFromRequest(r)

	origin := r.Header.Get("Origin")
	if !shorigin.ValidateDashboardOrigin(origin) {
		return nil, apierror.InvalidOriginHeader()
	}

	// only Dashboard V2
	returnURL := getReturnURL(r)
	return h.service.CreateApplicationCustomerPortalSession(r.Context(), appID, sessionID, origin, returnURL)
}

// POST /organizations/{organizationID}/customer_portal_session
func (h *HTTP) OrganizationCustomerPortalRedirect(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	organizationID := chi.URLParam(r, "organizationID")
	sessionID := getSessionIDFromRequest(r)
	returnURL := getReturnURL(r)

	origin := r.Header.Get("Origin")
	if !shorigin.ValidateDashboardOrigin(origin) {
		return nil, apierror.InvalidOriginHeader()
	}

	return h.service.CreateOrganizationCustomerPortalSession(r.Context(), organizationID, sessionID, origin, returnURL)
}

func getSessionIDFromRequest(r *http.Request) string {
	ctx := r.Context()
	claims, _ := sdkutils.GetActiveSession(ctx)
	return claims.SessionID
}

// POST /webhooks/stripe
func (h *HTTP) StripeWebhook(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	event, err := webhook.ConstructEvent(payload, r.Header.Get("Stripe-Signature"), cenv.Get(cenv.StripeWebhookSecret))
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	ctx := r.Context()

	switch event.Type {
	case "checkout.session.completed": // plan selection/Stripe Checkout
		var session stripe.CheckoutSession
		err := json.Unmarshal(event.Data.Raw, &session)
		if err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}
		apiErr := h.service.CheckoutSessionCompleted(ctx, session)
		return nil, apiErr

	case "customer.subscription.deleted":
		var subscription stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}
		apiErr := h.service.CustomerSubscriptionDeleted(ctx, subscription)
		return nil, apiErr

	case "customer.subscription.updated":
		var subscription stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &subscription); err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}

		var apiErr apierror.Error
		if subscription.Status == stripe.SubscriptionStatusCanceled ||
			subscription.Status == stripe.SubscriptionStatusIncompleteExpired {
			apiErr = h.service.CustomerSubscriptionDeleted(ctx, subscription)
		} else if subscription.Status == stripe.SubscriptionStatusActive ||
			subscription.Status == stripe.SubscriptionStatusTrialing {
			var previousItems []*stripe.SubscriptionItem
			if event.Data.PreviousAttributes != nil {
				items, ok := event.Data.PreviousAttributes["items"]
				if ok && items != nil {
					raw, err := json.Marshal(items.(map[string]any)["data"])
					if err != nil {
						return nil, apierror.InvalidRequestBody(err)
					}
					err = json.Unmarshal(raw, &previousItems)
					if err != nil {
						return nil, apierror.InvalidRequestBody(err)
					}
				}
			}
			apiErr = h.service.CustomerSubscriptionUpdated(ctx, subscription, previousItems)
		}
		return nil, apiErr

	case "customer.updated":
		var customer stripe.Customer
		if err := json.Unmarshal(event.Data.Raw, &customer); err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}

		apiErr := h.service.CustomerUpdated(ctx, &customer)
		return nil, apiErr

	case "customer.discount.created", "customer.discount.updated", "customer.discount.deleted":
		var discount stripe.Discount
		if err := json.Unmarshal(event.Data.Raw, &discount); err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}

		apiErr := h.service.CustomerDiscountUpdated(ctx, &discount)
		return nil, apiErr

	case "invoiceitem.created", "invoiceitem.updated", "invoiceitem.deleted":
		var invoiceItem stripe.InvoiceItem
		if err := json.Unmarshal(event.Data.Raw, &invoiceItem); err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}

		apiErr := h.service.InvoiceItemUpdated(ctx, &invoiceItem)
		return nil, apiErr
	}
	// all others are ignored
	return nil, nil
}

// POST /applications/{applicationID}/checkout/{planID}/validate
func (h *HTTP) OldCheckoutValidate(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.CheckoutValidate(r.Context(), CheckoutValidateParams{
		ApplicationID: chi.URLParam(r, "applicationID"),
		Plans:         []string{chi.URLParam(r, "planID")},
		WithRefund:    false,
	})
}

func (h *HTTP) CheckoutValidate(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params CheckoutValidateParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	params.ApplicationID = chi.URLParam(r, "applicationID")
	return h.service.CheckoutValidate(r.Context(), params)
}

// POST /applications/{applicationID}/checkout/suggest_addons
func (h *HTTP) CheckoutSuggestAddons(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params CheckoutSuggestAddonsParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	params.ApplicationID = chi.URLParam(r, "applicationID")
	return h.service.CheckoutSuggestAddons(r.Context(), params)
}

// GET /organizations/{organizationID}/checkout/{planID}/session
func (h *HTTP) CheckoutOrganizationSessionRedirect(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	organizationID := chi.URLParam(r, "organizationID")
	planID := chi.URLParam(r, "planID")
	sessionID := getSessionIDFromRequest(r)
	returnURL := getReturnURL(r)

	origin := r.Header.Get("Origin")
	if !shorigin.ValidateDashboardOrigin(origin) {
		return nil, apierror.InvalidOriginHeader()
	}

	return h.service.CreateOrganizationCheckoutSession(r.Context(), OrganizationCheckoutSessionParams{
		OrganizationID: organizationID,
		PlanID:         planID,
		SessionID:      sessionID,
		ReturnURL:      returnURL,
		Origin:         origin,
	})
}
