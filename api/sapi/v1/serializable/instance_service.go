package serializable

import (
	"context"

	"clerk/api/shared/instances"
	"clerk/model"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
)

type InstanceService struct {
	instanceService      *instances.Service
	subscriptionPlanRepo *repository.SubscriptionPlans
}

func NewInstanceService(db database.Database, gueClient *gue.Client) *InstanceService {
	return &InstanceService{
		instanceService:      instances.NewService(db, gueClient),
		subscriptionPlanRepo: repository.NewSubscriptionPlans(),
	}
}

type Instance struct {
	Env                    *model.Env
	BasicPlan              *model.SubscriptionPlan
	Addons                 []*model.SubscriptionPlan
	ActiveDomain           *model.Domain
	IsActiveDomainDeployed bool
}

func (s *InstanceService) ConvertToSerializable(ctx context.Context, exec database.Executor, env *model.Env) (*Instance, error) {
	serializable := &Instance{
		Env:          env,
		Addons:       []*model.SubscriptionPlan{},
		ActiveDomain: env.Domain,
	}

	plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, exec, env.Subscription.ID)
	if err != nil {
		return nil, err
	}
	for _, plan := range plans {
		if plan.IsAddon {
			serializable.Addons = append(serializable.Addons, plan)
		} else {
			serializable.BasicPlan = plan
		}
	}

	serializable.IsActiveDomainDeployed, err = s.instanceService.IsDeployed(ctx, env.Instance)
	if err != nil {
		return nil, err
	}

	return serializable, nil
}
