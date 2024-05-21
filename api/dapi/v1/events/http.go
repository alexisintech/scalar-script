package events

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/billing"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps, paymentProvider billing.PaymentProvider) *HTTP {
	return &HTTP{
		service: NewService(deps, paymentProvider),
	}
}

// POST /webhooks/clerk
func (h *HTTP) ClerkWebhook(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	event := ClerkEvent{}
	if err := clerkhttp.Decode(r, &event); err != nil {
		return nil, err
	}

	err := h.service.HandleClerkEvents(r.Context(), event)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}
