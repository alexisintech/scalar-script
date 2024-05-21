package applications

import (
	"context"
	"time"

	"clerk/api/apierror"
	"clerk/api/serialize"
	shpricing "clerk/api/shared/pricing"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	dailyUniqueActiveUser   *repository.DailyUniqueActiveUsers
	displayConfigRepo       *repository.DisplayConfig
	instanceRepo            *repository.Instances
	integrationRepo         *repository.Integrations
	subscriptionPlanRepo    *repository.SubscriptionPlans
	subscriptionProductRepo *repository.SubscriptionProduct
	subscriptionRepo        *repository.Subscriptions
}

func NewService() *Service {
	return &Service{
		dailyUniqueActiveUser:   repository.NewDailyUniqueActiveUsers(),
		displayConfigRepo:       repository.NewDisplayConfig(),
		instanceRepo:            repository.NewInstances(),
		integrationRepo:         repository.NewIntegrations(),
		subscriptionPlanRepo:    repository.NewSubscriptionPlans(),
		subscriptionProductRepo: repository.NewSubscriptionProduct(),
		subscriptionRepo:        repository.NewSubscriptions(),
	}
}

// HasRecentlyActiveProductionInstance returns true if the given application has activity in
// its production instance recently (i.e. the last month).
func (s *Service) HasRecentlyActiveProductionInstance(ctx context.Context, exec database.Executor, clock clockwork.Clock, appID string) (bool, error) {
	productionInstance, err := s.instanceRepo.QueryByApplicationAndEnvironmentType(ctx, exec, appID, constants.ETProduction)
	if err != nil {
		return false, err
	}
	if productionInstance == nil {
		return false, nil
	}

	now := clock.Now().UTC()
	aMonthAgo := now.Add(-30 * 24 * time.Hour)
	activeUsers, err := s.dailyUniqueActiveUser.CountForInstanceAndRange(ctx, exec, productionInstance.ID, aMonthAgo, now)
	return activeUsers > 0, err
}

func (s *Service) GetAvailableSubscriptionPlans(ctx context.Context, exec database.Executor, appID string) ([]*model.SubscriptionPlan, error) {
	subscription, err := s.subscriptionRepo.FindByResourceID(ctx, exec, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	subscriptionProducts, err := s.subscriptionProductRepo.FindAllBySubscriptionID(ctx, exec, subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	subscriptionPlanIDs := make([]string, len(subscriptionProducts))
	for i, sp := range subscriptionProducts {
		subscriptionPlanIDs[i] = sp.SubscriptionPlanID
	}

	availablePlans, err := s.subscriptionPlanRepo.FindAvailableForResource(ctx, exec, subscriptionPlanIDs, appID, constants.ApplicationResource)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return availablePlans, nil
}

func (s *Service) SerializeExtended(ctx context.Context, exec database.Executor, applications []*model.Application) ([]*serialize.ExtendedApplicationResponse, apierror.Error) {
	responses := make([]*serialize.ExtendedApplicationResponse, len(applications))
	for i, app := range applications {
		// PERF: we don't use toResponse() because a lot of the queries
		// it executes are irrelevant to this view.
		sm, apiErr := s.convertToSerializableMinimal(ctx, exec, app)
		if apiErr != nil {
			return nil, apiErr
		}

		serializable := &model.ApplicationSerializable{ApplicationSerializableMinimal: sm}
		currentPlan, err := shpricing.GetCurrentPlan(ctx, exec, s.subscriptionPlanRepo, sm.Subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		serializable.SubscriptionPlan = currentPlan

		responses[i] = serialize.ExtendedApplication(ctx, serializable)
	}

	return responses, nil
}

func (s *Service) convertToSerializableMinimal(ctx context.Context, exec database.Executor, app *model.Application) (*model.ApplicationSerializableMinimal, apierror.Error) {
	serializable := &model.ApplicationSerializableMinimal{Application: app}

	var err error
	serializable.Subscription, err = s.subscriptionRepo.FindByResourceID(ctx, exec, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializable.Instances, err = s.instanceRepo.FindAllByApplication(ctx, exec, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if len(serializable.Instances) > 0 {
		serializable.DisplayConfig, err = s.displayConfigRepo.FindByID(ctx, exec, serializable.Instances[0].ActiveDisplayConfigID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	serializable.IntegrationTypes, err = s.integrationRepo.FindAllTypesByApplication(ctx, exec, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serializable, nil
}
