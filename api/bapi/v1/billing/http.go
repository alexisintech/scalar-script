package billing

import (
	"io"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/billing"
	"clerk/utils/clerk"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps, billingConnector billing.Connector) *HTTP {
	return &HTTP{
		service: NewService(deps, billingConnector),
	}
}

// POST /v1/events/stripe
func (h *HTTP) StripeWebhook(w http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	// Protects against a malicious client streaming us an endless request body
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return nil, h.service.HandleStripeEvent(r.Context(), r.Header.Get("Stripe-Signature"), payload)
}
