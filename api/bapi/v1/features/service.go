package features

import (
	"context"

	"clerk/api/apierror"
	"clerk/pkg/billing"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	db                    database.Database
	subscriptionPlansRepo *repository.SubscriptionPlans
}

func NewService(db database.Database) *Service {
	return &Service{
		db:                    db,
		subscriptionPlansRepo: repository.NewSubscriptionPlans(),
	}
}

// CheckSupportedByPlan returns an error if the current application plan
// does not support the given feature.
func (s *Service) CheckSupportedByPlan(ctx context.Context, billingFeature string) apierror.Error {
	env := environment.FromContext(ctx)

	if env.Instance.HasAccessToAllFeatures() {
		return nil
	}

	plans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	unsupportedFeatures := billing.ValidateSupportedFeatures(set.New(billingFeature), env.Subscription, plans...)
	if len(unsupportedFeatures) > 0 {
		return apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
	}

	return nil
}
