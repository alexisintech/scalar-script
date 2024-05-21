package webhooks

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/pkg/externalapis/svix"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	svixClient *svix.Client

	// repositories
	applicationRepo      *repository.Applications
	instanceRepo         *repository.Instances
	subscriptionRepo     *repository.Subscriptions
	subscriptionPlanRepo *repository.SubscriptionPlans
}

func NewService(svixClient *svix.Client) *Service {
	return &Service{
		svixClient:           svixClient,
		applicationRepo:      repository.NewApplications(),
		instanceRepo:         repository.NewInstances(),
		subscriptionRepo:     repository.NewSubscriptions(),
		subscriptionPlanRepo: repository.NewSubscriptionPlans(),
	}
}

// CreateSvix calls Svix to create a new app and associate it with the current instance
func (s *Service) CreateSvix(ctx context.Context, tx database.Tx, instance *model.Instance) (*serialize.SvixURLResponse, error) {
	if instance.IsSvixEnabled() {
		// we only allow one Svix app per instance
		return nil, apierror.SvixAppAlreadyExists()
	}

	application, err := s.applicationRepo.FindByID(ctx, tx, instance.ApplicationID)
	if err != nil {
		return nil, err
	}

	appName := fmt.Sprintf("%s - %s", application.Name, instance.EnvironmentType)

	svixAppID, err := s.svixClient.Create(ctx, appName)
	if err != nil {
		return nil, err
	}

	instance.SvixAppID = null.StringFrom(svixAppID)
	err = s.instanceRepo.UpdateSvixAppID(ctx, tx, instance)
	if err != nil {
		return nil, err
	}

	authURL, err := s.svixClient.CreateAuthURL(svixAppID)
	if err != nil {
		return nil, err
	}

	return serialize.SvixURL(authURL), nil
}

// CreateSvixURL calls svix to create a new auth url for the current instance.
func (s *Service) CreateSvixURL(instance *model.Instance) (*serialize.SvixURLResponse, apierror.Error) {
	if !instance.IsSvixEnabled() {
		return nil, apierror.SvixAppMissing()
	}

	authURL, err := s.svixClient.CreateAuthURL(instance.SvixAppID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.SvixURL(authURL), nil
}

// DeleteSvix deletes the svix app associated with the given instance
func (s *Service) DeleteSvix(ctx context.Context, tx database.Tx, instance *model.Instance) error {
	if !instance.IsSvixEnabled() {
		return apierror.SvixAppMissing()
	}

	err := s.svixClient.Delete(instance.SvixAppID.String)
	if err != nil {
		return err
	}

	instance.SvixAppID = null.StringFromPtr(nil)
	return s.instanceRepo.UpdateSvixAppID(ctx, tx, instance)
}
