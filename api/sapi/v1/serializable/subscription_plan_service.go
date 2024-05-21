package serializable

import (
	"context"

	"clerk/model"
	"clerk/repository"
	"clerk/utils/database"
)

type SubscriptionPlanService struct {
	applicationRepo *repository.Applications
}

func NewSubscriptionPlanService() *SubscriptionPlanService {
	return &SubscriptionPlanService{
		applicationRepo: repository.NewApplications(),
	}
}

type SubscriptionPlan struct {
	SubscriptionPlan *model.SubscriptionPlan
	Applications     []*model.Application
}

func (s *SubscriptionPlanService) ConvertToSerializable(ctx context.Context, exec database.Executor, subscriptionPlan *model.SubscriptionPlan) (*SubscriptionPlan, error) {
	subscriptionPlanSerializables, err := s.ConvertAllToSerializable(ctx, exec, []*model.SubscriptionPlan{subscriptionPlan})
	if err != nil {
		return nil, err
	}
	return subscriptionPlanSerializables[0], nil
}

func (s *SubscriptionPlanService) ConvertAllToSerializable(ctx context.Context, exec database.Executor, subscriptionPlans []*model.SubscriptionPlan) ([]*SubscriptionPlan, error) {
	applicationIDs := make([]string, 0)
	for _, subscriptionPlan := range subscriptionPlans {
		applicationIDs = append(applicationIDs, subscriptionPlan.VisibleToApplicationIds...)
	}

	applications, err := s.applicationRepo.FindAllByIDs(ctx, exec, applicationIDs)
	if err != nil {
		return nil, err
	}
	applicationsMap := make(map[string]*model.Application)
	for _, application := range applications {
		applicationsMap[application.ID] = application
	}

	serializableSubscriptionPlans := make([]*SubscriptionPlan, len(subscriptionPlans))
	for i, subscriptionPlan := range subscriptionPlans {
		applicationsInPlan := make([]*model.Application, 0)
		for _, applicationID := range subscriptionPlan.VisibleToApplicationIds {
			application, exists := applicationsMap[applicationID]
			if !exists {
				continue
			}
			applicationsInPlan = append(applicationsInPlan, application)
		}
		serializableSubscriptionPlans[i] = &SubscriptionPlan{
			SubscriptionPlan: subscriptionPlan,
			Applications:     applicationsInPlan,
		}
	}
	return serializableSubscriptionPlans, nil
}
