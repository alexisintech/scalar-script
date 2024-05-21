package billing_current

import (
	"context"

	"clerk/model"
	"clerk/pkg/billing"

	"github.com/stripe/stripe-go/v72"
)

const ObjectName = "current_billing_plan"

// sharedSerializerFields defines shared fields for all SerializedBillingCurrent.
// It's for code generation only, not used in code.
//
// nolint:unused
type sharedSerializerFields struct {
	Object       string   `json:"object"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Key          string   `json:"key"`
	Description  string   `json:"description"`
	PriceInCents int64    `json:"price_in_cents"`
	Features     []string `json:"features"`
	CreatedAt    int64    `json:"created_at"`
	UpdatedAt    int64    `json:"updated_at"`

	// relations
	BillingCycle  *BillingCycle  `json:"billing_cycle"`
	PaymentMethod *PaymentMethod `json:"payment_method"`
}

type BillingCycle struct {
	StartDate int64 `json:"start_date"`
	EndDate   int64 `json:"end_date"`
}

type PaymentMethod struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Card      *Card  `json:"card"`
	CreatedAt int64  `json:"created_at"`
}

type Card struct {
	Brand    string `json:"brand"`
	Last4    string `json:"last4"`
	ExpMonth int64  `json:"exp_month"`
	ExpYear  int64  `json:"exp_year"`
}

// sharedSerializerMethods defines shared methods for all Serializer implementations.
// It's for code generation only, not used in code.
//
// nolint:unused
type sharedSerializerMethods interface {
	Serialize(
		ctx context.Context,
		subscription *model.BillingSubscription,
		plan *model.BillingPlan,
		nextInvoice *stripe.Invoice,
		paymentMethod *billing.PaymentMethod,
	) *SerializedBillingCurrent
}
