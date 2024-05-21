package serialize

import (
	"clerk/api/serialize"
	"clerk/model"
)

type BillingPlanResponse struct {
	*serialize.BillingPlanResponse
	StripeProductID *string `json:"stripe_product_id"`
	StripePriceID   *string `json:"stripe_price_id"`
}

func BillingPlan(plan *model.BillingPlan) *BillingPlanResponse {
	return &BillingPlanResponse{
		BillingPlanResponse: serialize.BillingPlan(plan),
		StripeProductID:     plan.StripeProductID.Ptr(),
		StripePriceID:       plan.StripePriceID.Ptr(),
	}
}
