package serialize

import (
	"fmt"
	"sort"
	"strings"

	"clerk/model"

	"github.com/stripe/stripe-go/v72"
)

type CheckoutResponse struct {
	SubscriptionID  string            `json:"subscription_id"`
	AmountDue       int64             `json:"amount_due"`
	StartingBalance int64             `json:"starting_balance"`
	Subtotal        int64             `json:"subtotal"`
	Total           int64             `json:"total"`
	Plans           []*CheckoutPlan   `json:"plans"`
	Discount        *CheckoutDiscount `json:"discount"`
	TrialStart      *int64            `json:"trial_start"`
	TrialEnd        *int64            `json:"trial_end"`
	ClientSecret    *string           `json:"client_secret"`
	ConfirmPayment  bool              `json:"confirm_payment"`
	PaymentStatus   string            `json:"payment_status"`
}

type CheckoutPlan struct {
	Name       string           `json:"name"`
	IsAddon    bool             `json:"is_addon"`
	BaseAmount int64            `json:"base_amount"`
	Prices     []*CheckoutPrice `json:"prices"`
}

type CheckoutPrice struct {
	Name  string          `json:"name"`
	Tiers []*CheckoutTier `json:"tiers"`
}

type CheckoutTier struct {
	Description string `json:"description"`
	Amount      int64  `json:"amount"`
	UpTo        *int64 `json:"up_to"`
}

type CheckoutDiscount struct {
	PercentOff float64 `json:"percent_off"`
	EndsAt     int64   `json:"ends_at"`
	Total      int64   `json:"total"`
}

func Checkout(
	clerkSubscription *model.Subscription,
	stripeSubscription *stripe.Subscription,
	plans []*model.SubscriptionPlan,
	prices []*model.SubscriptionPrice,
) *CheckoutResponse {
	res := &CheckoutResponse{
		SubscriptionID: clerkSubscription.ID,
		PaymentStatus:  clerkSubscription.PaymentStatus,
	}

	if stripeSubscription == nil {
		return res
	}

	res.setTotals(stripeSubscription)
	res.setDiscount(stripeSubscription.LatestInvoice)
	res.setTrial(stripeSubscription)
	res.setItems(stripeSubscription.Items.Data, plans, prices)

	if stripeSubscription.LatestInvoice != nil && stripeSubscription.LatestInvoice.PaymentIntent != nil {
		res.ClientSecret = &stripeSubscription.LatestInvoice.PaymentIntent.ClientSecret
		res.ConfirmPayment = true
	} else if stripeSubscription.PendingSetupIntent != nil {
		res.ClientSecret = &stripeSubscription.PendingSetupIntent.ClientSecret
	}

	return res
}

func (res *CheckoutResponse) setTotals(subscription *stripe.Subscription) {
	if subscription.LatestInvoice == nil {
		return
	}
	invoice := subscription.LatestInvoice
	res.AmountDue = invoice.AmountDue
	res.StartingBalance = invoice.StartingBalance
	res.Subtotal = invoice.Subtotal
	res.Total = invoice.Total

	if subscription.TrialStart > 0 {
		res.Subtotal = sumFixedPrices(subscription.Items.Data)
	}
}

func (res *CheckoutResponse) setDiscount(invoice *stripe.Invoice) {
	if invoice == nil || invoice.Discount == nil || invoice.Discount.Coupon == nil {
		return
	}

	res.Discount = &CheckoutDiscount{
		PercentOff: invoice.Discount.Coupon.PercentOff,
		EndsAt:     invoice.Discount.End,
	}
	var sum int64
	for _, d := range invoice.TotalDiscountAmounts {
		sum += d.Amount
	}
	res.Discount.Total = sum
}

func (res *CheckoutResponse) setTrial(subscription *stripe.Subscription) {
	if subscription.TrialStart > 0 {
		res.TrialStart = &subscription.TrialStart
	}
	if subscription.TrialEnd > 0 {
		res.TrialEnd = &subscription.TrialEnd
	}
}

func (res *CheckoutResponse) setItems(
	subItems []*stripe.SubscriptionItem,
	plans []*model.SubscriptionPlan,
	prices []*model.SubscriptionPrice,
) {
	// Group plans by Stripe ID for quick access later on.
	plansByStripeID := make(map[string]*model.SubscriptionPlan)
	for _, p := range plans {
		if !p.StripeProductID.Valid {
			continue
		}
		plansByStripeID[p.StripeProductID.String] = p
	}

	// Group prices by Stripe ID for quick access later on.
	pricesByStripeID := make(map[string]*model.SubscriptionPrice)
	for _, p := range prices {
		pricesByStripeID[p.StripePriceID] = p
	}

	// Clerk assumes that plans have many prices, but Stripe doesn't
	// follow the same convention. In Stripe, everything is a price
	// and the subscription item is a flat list of prices.
	// Let's group prices by product so we can get closer to the
	// Clerk pricing model (prices by plan) and make it easier
	// to build a Clerk based checkout summary.
	stripePricesByProduct := make(map[string][]*stripe.Price)
	for _, subitem := range subItems {
		if subitem.Price == nil || subitem.Price.Product == nil {
			continue
		}
		stripePricesByProduct[subitem.Price.Product.ID] = append(stripePricesByProduct[subitem.Price.Product.ID], subitem.Price)
	}

	// Now for each product, which is essentially a Clerk plan, let's break
	// down its prices.
	for stripeProductID, stripePrices := range stripePricesByProduct {
		plan, ok := plansByStripeID[stripeProductID]
		if !ok {
			continue
		}
		res.Plans = append(res.Plans, buildCheckoutPlan(plan, pricesByStripeID, stripePrices))
	}

	sort.Slice(res.Plans, func(i, j int) bool {
		posI, okI := plansOrder[res.Plans[i].Name]
		posJ, okJ := plansOrder[res.Plans[j].Name]
		return okI && okJ && posI < posJ
	})
}

// Build a CheckoutPlan along with its Prices list. Each Price
// can have one or more Tiers.
func buildCheckoutPlan(
	subscriptionPlan *model.SubscriptionPlan,
	clerkPricesByStripeID map[string]*model.SubscriptionPrice,
	prices []*stripe.Price,
) *CheckoutPlan {
	plan := &CheckoutPlan{
		Name:    subscriptionPlan.Title,
		IsAddon: subscriptionPlan.IsAddon,
		Prices:  make([]*CheckoutPrice, 0),
	}

	var smsPrice *CheckoutPrice
	for _, stripePrice := range prices {
		price, ok := clerkPricesByStripeID[stripePrice.ID]
		if !ok {
			continue
		}

		// Prices need to be handled differently depending on their type.
		// Prices for SMS usage need to become tiers of a fictional "SMS" price.
		// Fixed prices are considered the plan's base amount and they are
		// returned as a single tier.
		// Metered prices already have tiers, so their mapping is straightforward.
		if strings.Contains(price.Title.String, smsIdentifier) {
			smsPrice = buildSMSTier(smsPrice, price)
		} else if !price.Metered {
			plan.BaseAmount = int64(price.UnitAmount)
		} else {
			plan.Prices = append(plan.Prices, buildPriceWithTiers(price, stripePrice))
		}
	}

	// Let's add the fictional SMS price to the plan's prices if we
	// actually constructed it.
	if smsPrice != nil {
		plan.Prices = append(plan.Prices, sortSMSTiers(smsPrice))
	}

	// Sort all prices before returning the plan
	sort.Slice(plan.Prices, func(i, j int) bool {
		posI, okI := pricesOrder[plan.Prices[i].Name]
		posJ, okJ := pricesOrder[plan.Prices[j].Name]
		return okI && okJ && posI < posJ
	})

	return plan
}

const (
	maoIdentifier        = "MAOs"
	maoFullName          = "Monthly active organizations"
	mauIdentifier        = "MAUs"
	mauFullName          = "Monthly active users"
	samlIdentifier       = "SAML Connections"
	satellitesIdentifier = "Satellite Domains"
	smsIdentifier        = "SMS"

	proIdentifier           = "Pro"
	enhancedAuthIdentifier  = "Enhanced Authentication"
	enhancedB2BIdentifier   = "Enhanced B2B SaaS"
	enhancedAdminIdentifier = "Enhanced Administration"
)

var pricesOrder = map[string]int{
	mauFullName:          0,
	maoFullName:          1,
	smsIdentifier:        2,
	samlIdentifier:       3,
	satellitesIdentifier: 4,
}

var plansOrder = map[string]int{
	proIdentifier:           0,
	enhancedAuthIdentifier:  1,
	enhancedB2BIdentifier:   2,
	enhancedAdminIdentifier: 3,
}

// For our checkout summary, we want to treat SMS prices as one price
// with tiers, e.g. tier A, tier B, ...
// Since in reality each SMS tier is its own price, let's use the
// price title to detect SMS prices and add them to a fictional
// smsPrice as different tiers.
func buildSMSTier(smsPrice *CheckoutPrice, price *model.SubscriptionPrice) *CheckoutPrice {
	// Lazy initialization
	if smsPrice == nil {
		smsPrice = &CheckoutPrice{Name: smsIdentifier}
	}
	smsPrice.Tiers = append(smsPrice.Tiers, &CheckoutTier{
		Description: strings.Trim(strings.ReplaceAll(price.Title.String, smsIdentifier, ""), " "),
		Amount:      int64(price.UnitAmount),
	})
	return smsPrice
}

func buildPriceWithTiers(clerkPrice *model.SubscriptionPrice, stripePrice *stripe.Price) *CheckoutPrice {
	price := &CheckoutPrice{
		Name: fullPriceName(clerkPrice.Title.String),
	}
	if len(stripePrice.Tiers) > 0 {
		sort.Slice(stripePrice.Tiers, func(i, j int) bool {
			return stripePrice.Tiers[i].UpTo != 0 && stripePrice.Tiers[i].UpTo < stripePrice.Tiers[j].UpTo
		})
		for i, t := range stripePrice.Tiers {
			tier := &CheckoutTier{
				UpTo:   &t.UpTo,
				Amount: t.UnitAmount,
			}
			if t.UpTo != 0 || i == 0 {
				tier.Description = strings.Trim(fmt.Sprintf("First %d %s", t.UpTo, resourceForPriceName(clerkPrice.Title.String)), " ")
			} else {
				tier.Description = strings.Trim(fmt.Sprintf("%d+ %s", stripePrice.Tiers[i-1].UpTo+1, resourceForPriceName(clerkPrice.Title.String)), " ")
			}
			price.Tiers = append(price.Tiers, tier)
		}
	} else {
		price.Tiers = append(price.Tiers, &CheckoutTier{
			Description: "Billed monthly based on usage",
			Amount:      stripePrice.UnitAmount,
		})
	}
	return price
}

// Sort SMS tiers by name; tier A, tier B, etc.
func sortSMSTiers(smsPrice *CheckoutPrice) *CheckoutPrice {
	sort.Slice(smsPrice.Tiers, func(i, j int) bool {
		return smsPrice.Tiers[i].Description < smsPrice.Tiers[j].Description
	})
	return smsPrice
}

// Add all fixed price amounts. Metered prices are skipped.
func sumFixedPrices(subItems []*stripe.SubscriptionItem) int64 {
	var sum int64
	for _, subItem := range subItems {
		if subItem.Price == nil || subItem.Price.Recurring == nil || subItem.Price.Recurring.UsageType == stripe.PriceRecurringUsageTypeMetered {
			continue
		}
		sum += subItem.Price.UnitAmount
	}

	return sum
}

func fullPriceName(name string) string {
	switch name {
	case mauIdentifier:
		return mauFullName
	case maoIdentifier:
		return maoFullName
	default:
		return name
	}
}

func resourceForPriceName(name string) string {
	switch name {
	case mauIdentifier:
		return "users"
	case maoIdentifier:
		return "organizations"
	default:
		return ""
	}
}

// CheckoutNonPaid skips the checkout items, totals and discount.
func CheckoutNonPaid(subscription *model.Subscription) *CheckoutResponse {
	return &CheckoutResponse{
		SubscriptionID: subscription.ID,
		PaymentStatus:  subscription.PaymentStatus,
	}
}
