package serializable

import (
	"context"

	"clerk/model"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/database"
)

type ApplicationService struct {
	applicationOwnershipRepo *repository.ApplicationOwnerships
	subscriptionRepo         *repository.Subscriptions
	subscriptionPlanRepo     *repository.SubscriptionPlans
}

func NewApplicationService() *ApplicationService {
	return &ApplicationService{
		applicationOwnershipRepo: repository.NewApplicationOwnerships(),
		subscriptionRepo:         repository.NewSubscriptions(),
		subscriptionPlanRepo:     repository.NewSubscriptionPlans(),
	}
}

type Application struct {
	Application           *model.Application
	ApplicationOwnership  *model.ApplicationOwnership
	OwnerOrganizationPlan *model.SubscriptionPlan
}

func (s *ApplicationService) ConvertToSerializable(ctx context.Context, exec database.Executor, applications ...*model.Application) ([]*Application, error) {
	applicationOwnershipMap, err := s.resolveApplicationOwnerships(ctx, exec, applications)
	if err != nil {
		return nil, err
	}

	organizationIDs := make([]string, 0, len(applicationOwnershipMap))
	for _, applicationOwnership := range applicationOwnershipMap {
		if applicationOwnership.IsOrganization() {
			organizationIDs = append(organizationIDs, applicationOwnership.OwnerID())
		}
	}

	ownerOrganizationPlans, err := s.subscriptionPlanRepo.FindAllByResourceIDsAndType(ctx, exec, constants.OrganizationResource, organizationIDs...)
	if err != nil {
		return nil, err
	}

	serializableApplications := make([]*Application, len(applications))
	for i, application := range applications {
		var ownerOrganizationPlan *model.SubscriptionPlan
		if plan, ok := ownerOrganizationPlans[applicationOwnershipMap[application.ID].OwnerID()]; ok {
			ownerOrganizationPlan = plan
		}

		serializableApplications[i] = &Application{
			Application:           application,
			ApplicationOwnership:  applicationOwnershipMap[application.ID],
			OwnerOrganizationPlan: ownerOrganizationPlan,
		}
	}

	return serializableApplications, nil
}

func (s *ApplicationService) resolveApplicationOwnerships(ctx context.Context, exec database.Executor, applications []*model.Application) (map[string]*model.ApplicationOwnership, error) {
	applicationIDs := make([]string, len(applications))
	for i, application := range applications {
		applicationIDs[i] = application.ID
	}

	applicationOwnerships, err := s.applicationOwnershipRepo.FindByApplicationIDs(ctx, exec, applicationIDs)
	if err != nil {
		return nil, err
	}

	applicationOwnershipMap := make(map[string]*model.ApplicationOwnership, len(applicationOwnerships))
	for _, applicationOwnership := range applicationOwnerships {
		applicationOwnershipMap[applicationOwnership.ApplicationID] = applicationOwnership
	}
	return applicationOwnershipMap, nil
}
