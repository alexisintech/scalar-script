// Code generated by apiversioning CLI; DO NOT EDIT.

package billing_current

import (
	"clerk/model"
	"clerk/pkg/billing"
	"context"

	"github.com/stripe/stripe-go/v72"

	"clerk/pkg/apiversioning"
	apiversioningcontext "clerk/pkg/apiversioning/context"
)

// SerializedBillingCurrent fields is derived from sharedSerializerFields.
type SerializedBillingCurrent struct {
	Object        string         `json:"object"`
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Key           string         `json:"key"`
	Description   string         `json:"description"`
	PriceInCents  int64          `json:"price_in_cents"`
	Features      []string       `json:"features"`
	CreatedAt     int64          `json:"created_at"`
	UpdatedAt     int64          `json:"updated_at"`
	BillingCycle  *BillingCycle  `json:"billing_cycle"`
	PaymentMethod *PaymentMethod `json:"payment_method"`
}

// Serializer fields is derived from sharedSerializerMethods.
type Serializer interface {
	Serialize(
		ctx context.Context,
		subscription *model.BillingSubscription,
		plan *model.BillingPlan,
		nextInvoice *stripe.Invoice,
		paymentMethod *billing.PaymentMethod,
	) *SerializedBillingCurrent
}

type serializer struct {
	SerializedBillingCurrent20210205 Serializer
}

func NewSerializer() Serializer {
	return &serializer{}
}

func (s *serializer) Serialize(
	ctx context.Context,
	subscription *model.BillingSubscription,
	plan *model.BillingPlan,
	nextInvoice *stripe.Invoice,
	paymentMethod *billing.PaymentMethod,
) *SerializedBillingCurrent {
	activeSerializer := s.getSerializerForVersion(ctx)
	return activeSerializer.Serialize(ctx, subscription, plan, nextInvoice, paymentMethod)
}

func (s *serializer) getSerializerForVersion(ctx context.Context) Serializer {
	v, _ := apiversioningcontext.FromContext(ctx)

	if v.GTE(apiversioning.V20210205) {
		if s.SerializedBillingCurrent20210205 == nil {
			s.SerializedBillingCurrent20210205 = newSerializedBillingCurrent20210205()
		}
		return s.SerializedBillingCurrent20210205
	}

	return newSerializedBillingCurrent20210205()
}
