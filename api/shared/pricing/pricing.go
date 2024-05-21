package pricing

import (
	"context"
	"fmt"

	"clerk/model"
	"clerk/pkg/billing"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/database"
)

// GetCurrentPlan returns the active plan for the subscription, or an
// error if the subscription has no plan.
// Each subscription can have many products, but only one of them can
// be a plan. The rest are addons.
func GetCurrentPlan(
	ctx context.Context,
	exec database.Executor,
	subscriptionPlanRepo *repository.SubscriptionPlans,
	subscriptionID string,
) (*model.SubscriptionPlan, error) {
	enabledPlans, err := subscriptionPlanRepo.FindAllBySubscription(ctx, exec, subscriptionID)
	if err != nil {
		return nil, err
	}
	currentPlan := billing.DetectCurrentPlan(enabledPlans)
	if currentPlan == nil {
		return nil, fmt.Errorf("no plan for subscription %s", subscriptionID)
	}
	return currentPlan, nil
}

// UnsubcribeProducts removes the products from the subscription.
func UnsubcribeProducts(
	ctx context.Context,
	exec database.Executor,
	subscriptionProductRepo *repository.SubscriptionProduct,
	subscriptionID string,
	productIDs ...string,
) error {
	_, err := subscriptionProductRepo.DeleteBySubscriptionIDAndProductIDs(ctx, exec, subscriptionID, productIDs...)
	return err
}

// GetPricesForPlans returns all subscription prices for the provided plans.
func GetPricesForPlans(
	ctx context.Context,
	exec database.Executor,
	subscriptionPriceRepo *repository.SubscriptionPrices,
	plans ...*model.SubscriptionPlan,
) ([]*model.SubscriptionPrice, error) {
	stripeProductIDs := set.New[string]()
	for _, plan := range plans {
		if !plan.StripeProductID.Valid {
			continue
		}
		stripeProductIDs.Insert(plan.StripeProductID.String)
	}
	return subscriptionPriceRepo.FindAllByStripeProduct(ctx, exec, stripeProductIDs.Array()...)
}
