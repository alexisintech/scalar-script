package serialize

import (
	"clerk/model"
	clerkbilling "clerk/pkg/billing"
)

type SubscriptionPlanResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	DescriptionHTML string `json:"description_html,omitempty" logger:"omit"`
	IsAddon         bool   `json:"is_addon"`
}

func SubscriptionPlan(plan *model.SubscriptionPlan) *SubscriptionPlanResponse {
	return &SubscriptionPlanResponse{
		ID:              plan.ID,
		Name:            plan.Title,
		DescriptionHTML: plan.DescriptionHTML.String,
		IsAddon:         plan.IsAddon,
	}
}

type SubscriptionPlanWithPricesResponse struct {
	Object                      string                                `json:"object"`
	ID                          string                                `json:"id"`
	Name                        string                                `json:"name"`
	BaseAmount                  int64                                 `json:"base_amount"`
	MAOLimit                    int                                   `json:"mao_limit"`
	MAULimit                    int                                   `json:"mau_limit"`
	OrganizationMembershipLimit int                                   `json:"organization_membership_limit"`
	DescriptionHTML             string                                `json:"description_html,omitempty" logger:"omit"`
	Action                      string                                `json:"action"`
	IsAddon                     bool                                  `json:"is_addon"`
	IsEnterprise                bool                                  `json:"is_enterprise,omitempty"`
	Addons                      []*SubscriptionPlanWithPricesResponse `json:"addons,omitempty"`

	// deprecated
	Title string `json:"title"`
}

func WithAddons(addOns []*model.SubscriptionPlanWithPrices) func(*SubscriptionPlanWithPricesResponse) {
	return func(response *SubscriptionPlanWithPricesResponse) {
		addOnResponses := make([]*SubscriptionPlanWithPricesResponse, len(addOns))
		for i, addOn := range addOns {
			addOnResponses[i] = SubscriptionPlanWithPrices(addOn)
		}
		response.Addons = addOnResponses
	}
}

func WithAction(action string) func(*SubscriptionPlanWithPricesResponse) {
	return func(response *SubscriptionPlanWithPricesResponse) {
		response.Action = action
	}
}

func WithSubscriptionPlanEnterpriseIndication(isEnterprise bool) func(*SubscriptionPlanWithPricesResponse) {
	return func(response *SubscriptionPlanWithPricesResponse) {
		response.IsEnterprise = isEnterprise
	}
}

func SubscriptionPlanWithPrices(planWithPrices *model.SubscriptionPlanWithPrices, opts ...func(*SubscriptionPlanWithPricesResponse)) *SubscriptionPlanWithPricesResponse {
	response := &SubscriptionPlanWithPricesResponse{
		Object:                      "subscription_plan",
		ID:                          planWithPrices.Plan.ID,
		Name:                        planWithPrices.Plan.Title,
		BaseAmount:                  findBaseAmount(planWithPrices.Prices),
		MAOLimit:                    planWithPrices.Plan.MonthlyOrganizationLimit,
		MAULimit:                    planWithPrices.Plan.MonthlyUserLimit,
		OrganizationMembershipLimit: planWithPrices.Plan.OrganizationMembershipLimit,
		DescriptionHTML:             planWithPrices.Plan.DescriptionHTML.String,
		IsAddon:                     planWithPrices.Plan.IsAddon,
		Title:                       planWithPrices.Plan.Title,
	}
	for _, opt := range opts {
		opt(response)
	}
	return response
}

// This duplication is only temporary, until this file is also moved into
// DAPI serializers.
func findBaseAmount(prices []*model.SubscriptionPrice) int64 {
	for _, price := range prices {
		if price.Metric == clerkbilling.PriceTypes.Fixed {
			return int64(price.UnitAmount)
		}
	}
	return 0
}
