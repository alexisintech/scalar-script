package billing_current

import (
	"context"
	"time"

	"clerk/model"
	"clerk/pkg/billing"
	clerktime "clerk/pkg/time"

	"github.com/stripe/stripe-go/v72"
)

type SerializedBillingCurrent20210205 struct{}

func newSerializedBillingCurrent20210205() Serializer {
	return &SerializedBillingCurrent20210205{}
}

func (s *SerializedBillingCurrent20210205) Serialize(
	_ context.Context,
	subscription *model.BillingSubscription,
	plan *model.BillingPlan,
	nextInvoice *stripe.Invoice,
	paymentMethod *billing.PaymentMethod,
) *SerializedBillingCurrent {
	m := &SerializedBillingCurrent{
		Object:       ObjectName,
		ID:           subscription.ID,
		Name:         plan.Name,
		Key:          plan.Key,
		Description:  plan.Description.String,
		PriceInCents: plan.PriceInCents,
		Features:     plan.Features,
		CreatedAt:    clerktime.UnixMilli(subscription.CreatedAt),
		UpdatedAt:    clerktime.UnixMilli(subscription.UpdatedAt),
	}

	if nextInvoice != nil {
		m.BillingCycle = &BillingCycle{
			StartDate: clerktime.UnixMilli(time.Unix(nextInvoice.PeriodStart, 0)),
			EndDate:   clerktime.UnixMilli(time.Unix(nextInvoice.PeriodEnd, 0)),
		}
	}

	if paymentMethod != nil {
		m.PaymentMethod = &PaymentMethod{
			ID:        paymentMethod.ID,
			Type:      paymentMethod.Type,
			CreatedAt: clerktime.UnixMilli(paymentMethod.CreatedAt),
		}

		if paymentMethod.Card != nil {
			m.PaymentMethod.Card = &Card{
				Brand:    paymentMethod.Card.Brand,
				Last4:    paymentMethod.Card.Last4,
				ExpMonth: int64(paymentMethod.Card.ExpMonth),
				ExpYear:  int64(paymentMethod.Card.ExpYear),
			}
		}
	}

	return m
}
