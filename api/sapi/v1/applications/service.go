package applications

import (
	"context"
	"errors"

	"clerk/api/apierror"
	"clerk/api/sapi/serialize"
	"clerk/api/sapi/v1/serializable"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	applicationOwnershipRepo *repository.ApplicationOwnerships
	applicationRepo          *repository.Applications
	organizationRepo         *repository.Organization
	subscriptionPlanRepo     *repository.SubscriptionPlans
	subscriptionProductRepo  *repository.SubscriptionProduct
	subscriptionRepo         *repository.Subscriptions

	applicationService *serializable.ApplicationService
}

func NewService(db database.Database) *Service {
	return &Service{
		db:                       db,
		applicationOwnershipRepo: repository.NewApplicationOwnerships(),
		applicationRepo:          repository.NewApplications(),
		organizationRepo:         repository.NewOrganization(),
		subscriptionPlanRepo:     repository.NewSubscriptionPlans(),
		subscriptionProductRepo:  repository.NewSubscriptionProduct(),
		subscriptionRepo:         repository.NewSubscriptions(),
		applicationService:       serializable.NewApplicationService(),
	}
}

type ListApplicationsParams struct {
	Pagination pagination.Params
	Query      string
}

func (s *Service) ListApplications(ctx context.Context, params ListApplicationsParams) ([]*serialize.ApplicationResponse, apierror.Error) {
	if len(params.Query) == 0 {
		return nil, apierror.MissingQueryParameter("query")
	} else if len(params.Query) < 2 {
		// don't return any values if query doesn't contain at least 2 characters
		return []*serialize.ApplicationResponse{}, nil
	}
	applications, err := s.applicationRepo.FindAllNotSystemWithQuery(ctx, s.db, params.Query, params.Pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializableApplications, err := s.applicationService.ConvertToSerializable(ctx, s.db, applications...)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializedApplications := make([]*serialize.ApplicationResponse, len(applications))
	for i, serializableApplication := range serializableApplications {
		serializedApplications[i] = serialize.Application(serializableApplication)
	}

	return serializedApplications, nil
}

func (s *Service) Read(ctx context.Context, appID string) (any, apierror.Error) {
	application, err := s.applicationRepo.QueryByID(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if application == nil {
		return nil, apierror.ResourceNotFound()
	}

	serializableApplications, err := s.applicationService.ConvertToSerializable(ctx, s.db, application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Application(serializableApplications[0]), nil
}

type UpdateParams struct {
	ApplicationID     string
	HasUnlimitedSeats *bool `json:"has_unlimited_seats"`
}

func (s *Service) Update(ctx context.Context, params UpdateParams) (any, apierror.Error) {
	application, err := s.applicationRepo.QueryByID(ctx, s.db, params.ApplicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if application == nil {
		return nil, apierror.ResourceNotFound()
	}

	appOwner, err := s.applicationOwnershipRepo.QueryByApplicationID(ctx, s.db, params.ApplicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if params.HasUnlimitedSeats != nil {
		if err := s.updateHasUnlimitedSeats(ctx, appOwner, *params.HasUnlimitedSeats); err != nil {
			return nil, err
		}
	}

	serializableApplications, err := s.applicationService.ConvertToSerializable(ctx, s.db, application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Application(serializableApplications[0]), nil
}

func (s *Service) updateHasUnlimitedSeats(ctx context.Context, appOwner *model.ApplicationOwnership, val bool) apierror.Error {
	if !appOwner.IsOrganization() {
		return apierror.CannotSetUnlimitedSeatsForUserApplication()
	}

	organization, err := s.organizationRepo.FindByID(ctx, s.db, appOwner.OrganizationID.String)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if organization.MaxAllowedMemberships == 0 && !val {
		return apierror.CannotUnsetUnlimitedSeatsForOrganization()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		clerkSubscription, err := s.subscriptionRepo.FindByResourceIDForUpdate(ctx, tx, organization.ID)
		if err != nil {
			return true, err
		}

		// if the organization already has unlimited memberships, we don't need to do anything
		subscriptionPlans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, clerkSubscription.ID)
		if err != nil {
			return true, err
		}
		maxAllowedMemberships := model.MaxAllowedOrganizationMemberships(subscriptionPlans)
		if maxAllowedMemberships == 0 && !val {
			return true, apierror.CannotUnsetUnlimitedSeatsForOrganization()
		}

		availablePlans, err := s.subscriptionPlanRepo.FindAllAvailableByResourceType(ctx, tx, organization.ID, constants.OrganizationResource)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		var unlimitedMembershipsPlan *model.SubscriptionPlan
		for _, plan := range availablePlans {
			if plan.OrganizationMembershipLimit == model.UnlimitedMemberships {
				unlimitedMembershipsPlan = plan
				break
			}
		}

		if unlimitedMembershipsPlan == nil || !unlimitedMembershipsPlan.StripeProductID.Valid {
			return true, apierror.Unexpected(errors.New("no valid unlimited memberships plan found"))
		}

		// Add the unlimited memberships plan to the organization's subscription
		// without subscribe them to Stripe, so they get this plan for free
		if err := s.subscriptionProductRepo.Insert(ctx, tx, model.NewSubscriptionProduct(clerkSubscription.ID, unlimitedMembershipsPlan.ID)); err != nil {
			return true, err
		}

		organization.MaxAllowedMemberships = model.UnlimitedMemberships
		if err := s.organizationRepo.UpdateMaxAllowedMemberships(ctx, tx, organization); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}
		return apierror.Unexpected(txErr)
	}

	return nil
}
