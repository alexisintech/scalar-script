package billing

import (
	"context"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	db                      database.Database
	billingPlanRepo         *repository.BillingPlans
	billingSubscriptionRepo *repository.BillingSubscriptions
	organizationRepo        *repository.Organization
	userRepo                *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                      deps.DB(),
		billingPlanRepo:         repository.NewBillingPlans(),
		billingSubscriptionRepo: repository.NewBillingSubscriptions(),
		organizationRepo:        repository.NewOrganization(),
		userRepo:                repository.NewUsers(),
	}
}

func (s *Service) InitSubscriptionForUser(ctx context.Context, user *model.User) apierror.Error {
	initialPlan, err := s.billingPlanRepo.QueryInitialPlanByInstanceAndCustomerType(ctx, s.db, user.InstanceID, constants.BillingUserType)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if initialPlan == nil {
		return nil
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		subscription := &model.BillingSubscription{BillingSubscription: &sqbmodel.BillingSubscription{
			BillingPlanID: initialPlan.ID,
			ResourceID:    user.ID,
			InstanceID:    null.StringFrom(user.InstanceID),
		}}
		if err := s.billingSubscriptionRepo.Insert(ctx, tx, subscription); err != nil {
			return true, err
		}

		user.BillingSubscriptionID = null.StringFrom(subscription.ID)
		if err := s.userRepo.UpdateBillingSubscriptionID(ctx, tx, user); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	return nil
}

func (s *Service) EnsureSubscriptionForOrganization(ctx context.Context, orgID string) apierror.Error {
	hasSubscription, err := s.organizationRepo.ExistsByIDAndHasBillingSubscription(ctx, s.db, orgID)
	if err != nil {
		return apierror.Unexpected(err)
	} else if hasSubscription {
		return nil
	}

	org, err := s.organizationRepo.FindByID(ctx, s.db, orgID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if err := s.InitSubscriptionForOrganization(ctx, org); err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

func (s *Service) InitSubscriptionForOrganization(ctx context.Context, org *model.Organization) apierror.Error {
	initialPlan, err := s.billingPlanRepo.QueryInitialPlanByInstanceAndCustomerType(ctx, s.db, org.InstanceID, constants.BillingOrganizationType)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if initialPlan == nil {
		return nil
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		subscription := &model.BillingSubscription{BillingSubscription: &sqbmodel.BillingSubscription{
			BillingPlanID: initialPlan.ID,
			ResourceID:    org.ID,
			InstanceID:    null.StringFrom(org.InstanceID),
		}}
		if err := s.billingSubscriptionRepo.Insert(ctx, tx, subscription); err != nil {
			return true, err
		}

		org.BillingSubscriptionID = null.StringFrom(subscription.ID)
		if err := s.organizationRepo.UpdateBillingSubscriptionID(ctx, tx, org); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	return nil
}
