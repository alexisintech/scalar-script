package pricing

import (
	"context"

	"clerk/api/apierror"
	"clerk/model"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/slack"
	"clerk/pkg/jobs"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	db                    database.Database
	clock                 clockwork.Clock
	gueClient             *gue.Client
	paymentProvider       clerkbilling.PaymentProvider
	cachedPaymentProvider *clerkbilling.CachedPaymentProvider

	// repositories
	plansRepo        *repository.SubscriptionPlans
	pricesRepo       *repository.SubscriptionPrices
	subscriptionRepo *repository.Subscriptions
}

func NewService(deps clerk.Deps, paymentProvider clerkbilling.PaymentProvider) *Service {
	return &Service{
		db:                    deps.DB(),
		clock:                 deps.Clock(),
		gueClient:             deps.GueClient(),
		paymentProvider:       paymentProvider,
		cachedPaymentProvider: clerkbilling.NewCachedPaymentProvider(deps.Clock(), deps.DB(), paymentProvider),
		plansRepo:             repository.NewSubscriptionPlans(),
		pricesRepo:            repository.NewSubscriptionPrices(),
		subscriptionRepo:      repository.NewSubscriptions(),
	}
}

// SyncPlans syncs plans from stripe to the db
func (s *Service) SyncPlans(ctx context.Context) apierror.Error {
	plans, err := clerkbilling.GetSubscriptionPlans(s.paymentProvider)
	if err != nil {
		getPlansErr := err
		err := jobs.SendSlackAlert(ctx, s.gueClient,
			jobs.SlackAlertArgs{
				Webhook: constants.SlackBilling,
				Message: slack.Message{
					Title: "Failed to sync plans",
					Text:  err.Error(),
					Type:  slack.Error,
				},
			})
		if err != nil {
			// report error to sentry
			sentryclerk.CaptureException(ctx, err)
		}
		return apierror.Unexpected(getPlansErr)
	}

	allPlans := make([]*model.SubscriptionPlan, len(plans))
	allPrices := make([]*model.SubscriptionPrice, 0)
	for i, planWithPrices := range plans {
		allPlans[i] = planWithPrices.Plan
		allPrices = append(allPrices, planWithPrices.Prices...)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err = s.plansRepo.UpsertPlans(tx, allPlans)
		if err != nil {
			return true, err
		}

		err = s.pricesRepo.UpsertPrices(tx, allPrices)
		if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}
	return nil
}

func (s *Service) CreateUsageReportJobs(ctx context.Context) error {
	return jobs.CreateUsageReportJobs(ctx, s.gueClient)
}

func (s *Service) RefreshCacheResponses(ctx context.Context) apierror.Error {
	paidSubscriptions, err := s.subscriptionRepo.FindAllByStripeSubscriptionNotNull(ctx, s.db)
	if err != nil {
		return apierror.Unexpected(err)
	}

	cachedSubscriptionIDs, err := s.cachedPaymentProvider.GetCachedSubscriptionIDs(ctx)
	if err != nil {
		return apierror.Unexpected(err)
	}

	existingIDs := set.New(cachedSubscriptionIDs...)

	notCachedSubscriptions := make([]*model.Subscription, 0)
	for _, subscription := range paidSubscriptions {
		if !subscription.StripeSubscriptionID.Valid || existingIDs.Contains(subscription.StripeSubscriptionID.String) {
			continue
		}
		notCachedSubscriptions = append(notCachedSubscriptions, subscription)
	}

	for _, subscription := range notCachedSubscriptions {
		err = jobs.RefreshSubscriptionCache(ctx, s.gueClient, jobs.RefreshSubscriptionCacheArgs{
			StripeSubscriptionID: subscription.StripeSubscriptionID.String,
		})
		if err != nil {
			return apierror.Unexpected(err)
		}
	}
	return nil
}
