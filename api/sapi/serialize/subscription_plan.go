package serialize

import (
	"clerk/api/sapi/v1/serializable"
	"clerk/pkg/time"
)

type SubscriptionPlanApplicationResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type SubscriptionPlanResponse struct {
	ID              string                                 `json:"id"`
	Name            string                                 `json:"name"`
	StripeProductID string                                 `json:"stripe_product_id"`
	Applications    []*SubscriptionPlanApplicationResponse `json:"applications"`
	CreatedAt       int64                                  `json:"created_at"`
	UpdatedAt       int64                                  `json:"updated_at"`
}

func SubscriptionPlan(subscriptionPlan *serializable.SubscriptionPlan) *SubscriptionPlanResponse {
	applicationResponse := make([]*SubscriptionPlanApplicationResponse, len(subscriptionPlan.Applications))
	for i, application := range subscriptionPlan.Applications {
		applicationResponse[i] = &SubscriptionPlanApplicationResponse{
			ID:   application.ID,
			Name: application.Name,
		}
	}
	return &SubscriptionPlanResponse{
		ID:              subscriptionPlan.SubscriptionPlan.ID,
		Name:            subscriptionPlan.SubscriptionPlan.Title,
		StripeProductID: subscriptionPlan.SubscriptionPlan.StripeProductID.String,
		Applications:    applicationResponse,
		CreatedAt:       time.UnixMilli(subscriptionPlan.SubscriptionPlan.CreatedAt),
		UpdatedAt:       time.UnixMilli(subscriptionPlan.SubscriptionPlan.UpdatedAt),
	}
}
