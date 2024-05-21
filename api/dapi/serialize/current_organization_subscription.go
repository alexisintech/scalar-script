package serialize

import (
	"clerk/model"
)

const CurrentOrganizationSubscriptionObjectName = "organization_subscription"

type OrganizationSubscriptionMembers struct {
	BillingCycle *model.DateRange `json:"billing_cycle,omitempty"`
	Type         string           `json:"type"`
	Usage        *Usage           `json:"usage"`
}

type CurrentOrganizationSubscriptionResponse struct {
	Object       string                                    `json:"object"`
	TotalCredit  int64                                     `json:"total_credit"`
	Members      *OrganizationSubscriptionMembers          `json:"members"`
	Applications []*CurrentApplicationSubscriptionResponse `json:"applications"`
}

func WithMemberUsage(subscription *model.Subscription, billingCycle model.DateRange, usage *model.Usage) func(*CurrentOrganizationSubscriptionResponse) {
	return func(response *CurrentOrganizationSubscriptionResponse) {
		response.Members.BillingCycle = &billingCycle
		response.Members.Type = determineOrganizationSubscriptionType(subscription)
		response.Members.Usage = &Usage{
			AmountDue:  usage.AmountDue,
			TotalUnits: usage.TotalUnits,
			HardLimit:  usage.HardLimit,
			FreeLimit:  usage.FreeLimit,
		}
	}
}

func CurrentOrganizationSubscription(totalCredit int64, applicationSubscriptions []*CurrentApplicationSubscriptionResponse, opts ...func(*CurrentOrganizationSubscriptionResponse)) *CurrentOrganizationSubscriptionResponse {
	response := &CurrentOrganizationSubscriptionResponse{
		Object:       CurrentOrganizationSubscriptionObjectName,
		TotalCredit:  totalCredit,
		Applications: applicationSubscriptions,
		Members:      &OrganizationSubscriptionMembers{},
	}
	for _, opt := range opts {
		opt(response)
	}
	return response
}

func determineOrganizationSubscriptionType(organizationSubscription *model.Subscription) string {
	if !organizationSubscription.StripeSubscriptionID.Valid {
		return "free"
	}
	return "paid"
}
