package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const BillingPlanObjectName = "plan"

type BillingPlanResponse struct {
	Object       string   `json:"object"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Key          string   `json:"key"`
	Description  *string  `json:"description"`
	PriceInCents int64    `json:"price_in_cents"`
	Features     []string `json:"features"`
	CreatedAt    int64    `json:"created_at"`
	UpdatedAt    int64    `json:"updated_at"`
}

func BillingPlan(plan *model.BillingPlan) *BillingPlanResponse {
	return &BillingPlanResponse{
		Object:       BillingPlanObjectName,
		ID:           plan.ID,
		Name:         plan.Name,
		Key:          plan.Key,
		Description:  plan.Description.Ptr(),
		PriceInCents: plan.PriceInCents,
		Features:     plan.Features,
		CreatedAt:    time.UnixMilli(plan.CreatedAt),
		UpdatedAt:    time.UnixMilli(plan.UpdatedAt),
	}
}
