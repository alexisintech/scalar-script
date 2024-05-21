package serialize

import (
	"time"

	"clerk/model"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/set"
	clerktime "clerk/pkg/time"
)

const CurrentApplicationSubscriptionObjectName = "application_subscription"

type ApplicationSubscriptionProduct struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	DescriptionHTML string   `json:"description_html,omitempty" logger:"omit"`
	Features        []string `json:"features"`
	BaseAmount      int64    `json:"base_amount"`
	IsSubscribed    bool     `json:"is_subscribed"`
}

type Usage struct {
	AmountDue       int64 `json:"amount_due"`
	TotalUnits      int64 `json:"total_units"`
	HardLimit       int64 `json:"hard_limit"`
	FreeLimit       int64 `json:"free_limit"`
	IsUsed          bool  `json:"is_used"`
	PendingDeletion bool  `json:"pending_deletion"`
}

type ApplicationSubscriptionUsage struct {
	MAUs            Usage `json:"mau"`
	MAOs            Usage `json:"mao"`
	Domains         Usage `json:"domains"`
	SAMLConnections Usage `json:"saml_connections"`
	SMSTierA        Usage `json:"sms_tier_a"`
	SMSTierB        Usage `json:"sms_tier_b"`
	SMSTierC        Usage `json:"sms_tier_c"`
	SMSTierD        Usage `json:"sms_tier_d"`
	SMSTierE        Usage `json:"sms_tier_e"`
	SMSTierF        Usage `json:"sms_tier_f"`
	LastUpdatedAt   int64 `json:"last_updated_at"`
}

type DiscountInfo struct {
	PercentOff float64 `json:"percent_off"`
	EndsAt     int64   `json:"ends_at"`
}

type CurrentApplicationSubscriptionResponse struct {
	ApplicationID               string                           `json:"application_id"`
	Object                      string                           `json:"object"`
	Name                        string                           `json:"name"`
	Type                        string                           `json:"type"`
	Plan                        ApplicationSubscriptionProduct   `json:"plan"`
	Addons                      []ApplicationSubscriptionProduct `json:"addons"`
	Discount                    DiscountInfo                     `json:"discount"`
	BillingCycle                *model.DateRange                 `json:"billing_cycle,omitempty"`
	TotalAmount                 int64                            `json:"total_amount"`
	AmountDue                   int64                            `json:"amount_due"`
	CreditBalance               int64                            `json:"credit_balance"`
	Usage                       ApplicationSubscriptionUsage     `json:"usage"`
	InGracePeriod               bool                             `json:"in_grace_period"`
	UnsupportedFeatures         []string                         `json:"unsupported_features"`
	OrganizationMembershipLimit int                              `json:"organization_membership_limit"`
	HasBillingAccount           bool                             `json:"has_billing_account"`
}

func WithUsage(totalAmount, amountDue, creditBalance int64, invoiceDiscount *model.Discount, lastUpdatedAt time.Time, usages []*model.Usage) func(*CurrentApplicationSubscriptionResponse) {
	return func(response *CurrentApplicationSubscriptionResponse) {
		response.TotalAmount = totalAmount
		response.AmountDue = amountDue
		response.CreditBalance = creditBalance
		if invoiceDiscount != nil {
			response.Discount.PercentOff = invoiceDiscount.PercentOff
			response.Discount.EndsAt = clerktime.UnixMilli(invoiceDiscount.EndsAt)
		}
		response.Usage.LastUpdatedAt = clerktime.UnixMilli(lastUpdatedAt)

		for _, usage := range usages {
			newUsage := Usage{
				AmountDue:  usage.AmountDue,
				TotalUnits: usage.TotalUnits,
				HardLimit:  usage.HardLimit,
				FreeLimit:  usage.FreeLimit,
				IsUsed:     true,
			}
			switch usage.Metric {
			case clerkbilling.PriceTypes.MAU:
				response.Usage.MAUs = newUsage
			case clerkbilling.PriceTypes.MAO:
				response.Usage.MAOs = newUsage
			case clerkbilling.PriceTypes.Domains:
				response.Usage.Domains = newUsage
			case clerkbilling.PriceTypes.SAMLConnections:
				response.Usage.SAMLConnections = newUsage
			case clerkbilling.PriceTypes.SMSMessagesTierA:
				response.Usage.SMSTierA = newUsage
			case clerkbilling.PriceTypes.SMSMessagesTierB:
				response.Usage.SMSTierB = newUsage
			case clerkbilling.PriceTypes.SMSMessagesTierC:
				response.Usage.SMSTierC = newUsage
			case clerkbilling.PriceTypes.SMSMessagesTierD:
				response.Usage.SMSTierD = newUsage
			case clerkbilling.PriceTypes.SMSMessagesTierE:
				response.Usage.SMSTierE = newUsage
			case clerkbilling.PriceTypes.SMSMessagesTierF:
				response.Usage.SMSTierF = newUsage
			}
		}
	}
}

func WithBillingCycle(billingCycle model.DateRange) func(*CurrentApplicationSubscriptionResponse) {
	return func(response *CurrentApplicationSubscriptionResponse) {
		response.BillingCycle = &billingCycle
	}
}

func WithSubscriptionAddons(addons []*model.SubscriptionPlanWithPrices, subscribedPlans []*model.SubscriptionPlan) func(*CurrentApplicationSubscriptionResponse) {
	subscribedPlanIDs := set.New[string]()
	for _, subscribedPlan := range subscribedPlans {
		subscribedPlanIDs.Insert(subscribedPlan.ID)
	}
	return func(response *CurrentApplicationSubscriptionResponse) {
		serializedAddons := make([]ApplicationSubscriptionProduct, len(addons))
		for i, addon := range addons {
			serializedAddons[i] = ApplicationSubscriptionProduct{
				ID:           addon.Plan.ID,
				Name:         addon.Plan.Title,
				Features:     addon.Plan.Features,
				IsSubscribed: subscribedPlanIDs.Contains(addon.Plan.ID),
				BaseAmount:   findBaseAmount(addon.Prices),
			}
		}
		response.Addons = serializedAddons
	}
}

func CurrentSubscriptionGracePeriod(isInGracePeriod bool) func(*CurrentApplicationSubscriptionResponse) {
	return func(response *CurrentApplicationSubscriptionResponse) {
		response.InGracePeriod = isInGracePeriod
	}
}

func CurrentSubscriptionOrganizationMembershipLimit(limit int) func(*CurrentApplicationSubscriptionResponse) {
	return func(response *CurrentApplicationSubscriptionResponse) {
		response.OrganizationMembershipLimit = limit
	}
}

func CurrentSubscriptionHasBillingAccount(hasBillingAccount bool) func(*CurrentApplicationSubscriptionResponse) {
	return func(response *CurrentApplicationSubscriptionResponse) {
		response.HasBillingAccount = hasBillingAccount
	}
}

func CurrentApplicationSubscription(appID string, name string, subscription *model.Subscription, subscriptionPlanWithPrices *model.SubscriptionPlanWithPrices, opts ...func(*CurrentApplicationSubscriptionResponse)) *CurrentApplicationSubscriptionResponse {
	response := &CurrentApplicationSubscriptionResponse{
		Object:        CurrentApplicationSubscriptionObjectName,
		ApplicationID: appID,
		Name:          name,
		Plan: ApplicationSubscriptionProduct{
			ID:              subscriptionPlanWithPrices.Plan.ID,
			Name:            subscriptionPlanWithPrices.Plan.Title,
			DescriptionHTML: subscriptionPlanWithPrices.Plan.DescriptionHTML.String,
			Features:        subscriptionPlanWithPrices.Plan.Features,
			IsSubscribed:    true,
			BaseAmount:      findBaseAmount(subscriptionPlanWithPrices.Prices),
		},
		Type:                determineSubscriptionType(subscriptionPlanWithPrices),
		UnsupportedFeatures: subscription.GracePeriodFeatures,
	}
	for _, opt := range opts {
		opt(response)
	}
	return response
}

func determineSubscriptionType(subscription *model.SubscriptionPlanWithPrices) string {
	if subscription.IsFree() {
		return "free"
	} else if subscription.IsLegacy() {
		return "legacy"
	} else if subscription.IsPro() {
		return "pro"
	}
	return "custom"
}

func findBaseAmount(prices []*model.SubscriptionPrice) int64 {
	for _, price := range prices {
		if price.Metric == clerkbilling.PriceTypes.Fixed {
			return int64(price.UnitAmount)
		}
	}
	return 0
}
