package serialize

import "clerk/model"

type SubscriptionResponse struct {
	Object                      string `json:"object"`
	Plan                        string `json:"subscription_plan"`
	IsPaid                      bool   `json:"is_paid"`
	OrganizationMembershipLimit *int   `json:"organization_membership_limit,omitempty"`
}

func WithOrganizationMembershipLimit(limit int) func(*SubscriptionResponse) {
	return func(response *SubscriptionResponse) {
		response.OrganizationMembershipLimit = &limit
	}
}

func Subscription(
	subscription *model.Subscription,
	currentPlan *model.SubscriptionPlan,
	options ...func(*SubscriptionResponse),
) *SubscriptionResponse {
	response := &SubscriptionResponse{
		Object: "subscription",
		Plan:   currentPlan.ID,
		IsPaid: subscription.StripeSubscriptionID.Valid,
	}

	for _, option := range options {
		option(response)
	}

	return response
}
