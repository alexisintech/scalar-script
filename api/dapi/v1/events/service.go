package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"clerk/api/apierror"
	"clerk/api/shared/usage"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/events"
	"clerk/pkg/jobs"
	sentryclerk "clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock           clockwork.Clock
	db              database.Database
	gueClient       *gue.Client
	paymentProvider billing.PaymentProvider
	usageService    *usage.Service

	// repositories
	billingAccountsRepo     *repository.BillingAccounts
	stripeUsageRepo         *repository.StripeUsageReports
	subscriptionRepo        *repository.Subscriptions
	subscriptionPlanRepo    *repository.SubscriptionPlans
	subscriptionProductRepo *repository.SubscriptionProduct
}

func NewService(deps clerk.Deps, paymentProvider billing.PaymentProvider) *Service {
	return &Service{
		clock:                   deps.Clock(),
		db:                      deps.DB(),
		gueClient:               deps.GueClient(),
		paymentProvider:         paymentProvider,
		usageService:            usage.NewService(deps.Clock(), deps.DB(), deps.GueClient(), paymentProvider),
		billingAccountsRepo:     repository.NewBillingAccounts(),
		stripeUsageRepo:         repository.NewStripeUsageReports(),
		subscriptionRepo:        repository.NewSubscriptions(),
		subscriptionPlanRepo:    repository.NewSubscriptionPlans(),
		subscriptionProductRepo: repository.NewSubscriptionProduct(),
	}
}

type ClerkEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// HandleClerkEvents handles all events coming from the Clerk platform.
func (s *Service) HandleClerkEvents(ctx context.Context, event ClerkEvent) apierror.Error {
	switch event.Type {
	case events.EventTypes.OrganizationCreated.Name:
		var organization sdk.Organization
		err := json.Unmarshal(event.Data, &organization)
		if err != nil {
			return apierror.Unexpected(err)
		}
		return s.handleOrganizationCreated(ctx, organization)
	case events.EventTypes.OrganizationDeleted.Name, events.EventTypes.UserDeleted.Name:
		var deletedResource sdk.DeletedResource
		err := json.Unmarshal(event.Data, &deletedResource)
		if err != nil {
			return apierror.Unexpected(err)
		}
		return s.handleResourceDeleted(ctx, deletedResource)
	case events.EventTypes.UserCreated.Name:
		var user sdk.User
		err := json.Unmarshal(event.Data, &user)
		if err != nil {
			return apierror.Unexpected(err)
		}
		return s.handleUserCreated(ctx, user)
	case events.EventTypes.OrganizationMembershipCreated.Name:
		var organizationMembership sdk.OrganizationMembership
		if err := json.Unmarshal(event.Data, &organizationMembership); err != nil {
			return apierror.Unexpected(err)
		}
		usageByMetrics, err := s.usageService.ReportOrgUsageToStripe(ctx, s.db, organizationMembership.Organization.ID)
		if err != nil {
			return apierror.Unexpected(err)
		}
		newReport := &model.StripeUsageReport{
			StripeUsageReport: &sqbmodel.StripeUsageReport{
				UsageByMetric: usageByMetrics,
				SentAt:        s.clock.Now().UTC(),
				ResourceID:    organizationMembership.Organization.ID,
				ResourceType:  constants.OrganizationResource,
			},
		}
		err = s.stripeUsageRepo.Insert(ctx, s.db, newReport)
		if err != nil {
			return apierror.Unexpected(err)
		}
		return nil
	default:
		return apierror.Unexpected(fmt.Errorf("unknown event type %s", event.Type))
	}
}

func (s *Service) handleOrganizationCreated(ctx context.Context, organization sdk.Organization) apierror.Error {
	// assign free subscription to organization
	freePlan, err := s.subscriptionPlanRepo.FindFirstAvailableAndFreeByResourceType(ctx, s.db, constants.OrganizationResource)
	if err != nil {
		return apierror.Unexpected(err)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		subscription := &model.Subscription{
			Subscription: &sqbmodel.Subscription{
				ResourceID:   organization.ID,
				ResourceType: constants.OrganizationResource,
			},
		}
		err := s.subscriptionRepo.Insert(ctx, tx, subscription)
		if err != nil {
			return true, err
		}
		err = s.subscriptionProductRepo.Insert(ctx, tx, model.NewSubscriptionProduct(subscription.ID, freePlan.ID))
		if err != nil {
			return true, err
		}

		err = s.createStripeCustomer(ctx, tx, organization.ID, organization.Name)
		return err != nil, err
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	return nil
}

func (s *Service) handleResourceDeleted(ctx context.Context, deletedResource sdk.DeletedResource) apierror.Error {
	err := jobs.CleanupStripeCustomer(ctx, s.gueClient, jobs.CleanupStripeCustomerArgs{
		ResourceID: deletedResource.ID,
	})
	if err != nil {
		return apierror.Unexpected(fmt.Errorf("events: failed to enqueue Stripe customer cleanup job for %s: %w",
			deletedResource.ID, err))
	}
	return nil
}

func (s *Service) handleUserCreated(ctx context.Context, user sdk.User) apierror.Error {
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		name := fmt.Sprintf("%s %s", null.StringFromPtr(user.FirstName).String, null.StringFromPtr(user.LastName).String)
		err := s.createStripeCustomer(ctx, tx, user.ID, strings.TrimSpace(name))
		return err != nil, err
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}
	return nil
}

func (s *Service) createStripeCustomer(ctx context.Context, tx database.Tx, ownerID, name string) error {
	newCustomer, err := s.paymentProvider.CreateCustomer(name, ownerID)
	if err != nil {
		// don't fail, just report the fact that we failed to create the customer
		sentryclerk.CaptureException(ctx, err)
		return nil
	}
	billingAccount := &model.BillingAccount{
		BillingAccount: &sqbmodel.BillingAccount{
			OwnerID:          ownerID,
			StripeCustomerID: null.StringFrom(newCustomer.ID),
		},
	}
	return s.billingAccountsRepo.Insert(ctx, tx, billingAccount)
}
