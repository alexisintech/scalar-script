package usage

import (
	"context"
	"time"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/jobs"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	clock           clockwork.Clock
	gueClient       *gue.Client
	paymentProvider billing.PaymentProvider

	// repositories
	dailyAggregationsRepo      *repository.DailyAggregations
	domainRepo                 *repository.Domain
	instanceRepo               *repository.Instances
	organizationMembershipRepo *repository.OrganizationMembership
	samlConnectionsRepo        *repository.SAMLConnection
	stripeUsageRepo            *repository.StripeUsageReports
	subscriptionRepo           *repository.Subscriptions
	subscriptionMetricRepo     *repository.SubscriptionMetrics
}

func NewService(clock clockwork.Clock, db database.Database, gueClient *gue.Client, paymentProvider billing.PaymentProvider) *Service {
	return &Service{
		clock:                      clock,
		gueClient:                  gueClient,
		paymentProvider:            billing.NewCachedPaymentProvider(clock, db, paymentProvider),
		dailyAggregationsRepo:      repository.NewDailyAggregations(),
		domainRepo:                 repository.NewDomain(),
		instanceRepo:               repository.NewInstances(),
		organizationMembershipRepo: repository.NewOrganizationMembership(),
		samlConnectionsRepo:        repository.NewSAMLConnection(),
		stripeUsageRepo:            repository.NewStripeUsageReports(),
		subscriptionRepo:           repository.NewSubscriptions(),
		subscriptionMetricRepo:     repository.NewSubscriptionMetrics(),
	}
}

const (
	timeBetweenReports = 2 * time.Hour
	timeBeforeCycleEnd = 1 * time.Hour // assumes we report every hour
)

func (s *Service) ReportOrgUsageToStripe(ctx context.Context, readOnlyExec database.Executor, organizationID string) (map[string]int64, error) {
	now := s.clock.Now().UTC()
	subscription, err := s.subscriptionRepo.QueryByResourceID(ctx, readOnlyExec, organizationID)
	if err != nil {
		return nil, err
	}
	if subscription == nil {
		return nil, nil
	}

	usageByMetric := make(map[string]int64)

	subscriptionMetrics, err := s.subscriptionMetricRepo.FindAllBySubscriptionID(ctx, readOnlyExec, subscription.ID)
	if err != nil {
		return nil, err
	}

	for _, subscriptionMetric := range subscriptionMetrics {
		switch subscriptionMetric.Metric {
		case billing.PriceTypes.Seats:
			usageByMetric[subscriptionMetric.Metric], err = s.organizationMembershipRepo.CountByOrganization(ctx, readOnlyExec, organizationID)
			if err != nil {
				return nil, err
			}

			err = s.paymentProvider.ReportUsage(ctx, subscriptionMetric.StripeSubscriptionItemID, usageByMetric[subscriptionMetric.Metric], now)
			if err != nil {
				return nil, err
			}
		}
	}
	return usageByMetric, nil
}

func (s *Service) ReportAppUsageToStripe(ctx context.Context, readOnlyExec database.Executor, applicationID string, force bool) (map[string]int64, error) {
	now := s.clock.Now().UTC()

	subscription, err := s.subscriptionRepo.QueryByResourceID(ctx, readOnlyExec, applicationID)
	if err != nil {
		return nil, err
	}
	if subscription == nil {
		return nil, nil
	}

	lastReport, err := s.stripeUsageRepo.QueryLatestByResourceID(ctx, readOnlyExec, applicationID)
	if err != nil {
		return nil, err
	}

	currentCycle := model.CurrentBillingCycleFromAnchor(
		subscription.BillingCycleAnchor.Time, now,
	)
	// Don't send if last one was sent less than timeBetweenReports ago and
	// we're more than timeBeforeCycleEnd away from the end of the cycle, unless
	// we're forcing it.
	if !force &&
		lastReport != nil &&
		now.Sub(lastReport.SentAt) < timeBetweenReports &&
		currentCycle.End.Sub(now) > timeBeforeCycleEnd {
		return nil, nil
	}

	usageByMetrics := make(map[string]int64)

	prodInstance, err := s.instanceRepo.QueryByApplicationAndEnvironmentType(ctx, readOnlyExec, applicationID, constants.ETProduction)
	if err != nil {
		return nil, err
	}
	if prodInstance == nil {
		return nil, nil
	}

	subscriptionMetrics, err := s.subscriptionMetricRepo.FindAllBySubscriptionID(ctx, readOnlyExec, subscription.ID)
	if err != nil {
		return nil, err
	}

	for _, subscriptionMetric := range subscriptionMetrics {
		switch subscriptionMetric.Metric {
		case billing.PriceTypes.Domains:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportDomainsUsage(ctx, readOnlyExec,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.MAU:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportMeteredUsage(ctx, readOnlyExec, constants.UserAggregationType, currentCycle,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.MAO:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportMeteredUsage(ctx, readOnlyExec, constants.OrganizationAggregationType, currentCycle,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.SMSMessagesTierA:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportMeteredUsage(ctx, readOnlyExec, constants.SMSTierAAggregationType, currentCycle,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.SMSMessagesTierB:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportMeteredUsage(ctx, readOnlyExec, constants.SMSTierBAggregationType, currentCycle,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.SMSMessagesTierC:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportMeteredUsage(ctx, readOnlyExec, constants.SMSTierCAggregationType, currentCycle,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.SMSMessagesTierD:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportMeteredUsage(ctx, readOnlyExec, constants.SMSTierDAggregationType, currentCycle,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.SMSMessagesTierE:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportMeteredUsage(ctx, readOnlyExec, constants.SMSTierEAggregationType, currentCycle,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.SMSMessagesTierF:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportMeteredUsage(ctx, readOnlyExec, constants.SMSTierFAggregationType, currentCycle,
				subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		case billing.PriceTypes.SAMLConnections:
			usageByMetrics[subscriptionMetric.Metric], err = s.reportSAMLConnectionsUsage(ctx, readOnlyExec, currentCycle, subscriptionMetric.StripeSubscriptionItemID, prodInstance.ID)
			if err != nil {
				return nil, err
			}
		}
	}

	if cenv.IsEnabled(cenv.FlagAllowPricingV2Cache) {
		// Refresh the cache for the subscription to keep the cache up to date because
		// we just reported usage, which invalidates the cache for the subscription.
		if err := jobs.RefreshSubscriptionCache(ctx, s.gueClient, jobs.RefreshSubscriptionCacheArgs{
			StripeSubscriptionID: subscription.StripeSubscriptionID.String,
		}); err != nil {
			return nil, err
		}
	}
	return usageByMetrics, nil
}

// To calculate the SAML Connections usage we are following a bit of different logic than we do for the existing
// metered usage. Instead of solely depend on the aggregations of SAML connection activations, we're also taking
// into account the current active SAML Connections.
// If we don't do this, then while are calculating the usage of the current
// billing cycle, we won't include any SAML Connections, which weren't enabled/disabled during this cycle, but
// where created during older billing cycles.
func (s *Service) reportSAMLConnectionsUsage(ctx context.Context, exec database.Executor, currentCycle model.DateRange, subscriptionItemID, instanceID string) (int64, error) {
	now := s.clock.Now().UTC()

	samlConnections, err := s.samlConnectionsRepo.FindAllActiveByInstanceID(ctx, exec, instanceID)
	if err != nil {
		return 0, apierror.Unexpected(err)
	}

	samlActivations, err := s.dailyAggregationsRepo.FindAllByTypeInstanceAndTimeRange(
		ctx,
		exec,
		constants.SAMLActivationAggregationType,
		instanceID,
		currentCycle.Start,
		now)
	if err != nil {
		return 0, apierror.Unexpected(err)
	}

	totalConnectionIDs := set.New[string]()
	for _, connection := range samlConnections {
		totalConnectionIDs.Insert(connection.ID)
	}
	for _, activation := range samlActivations {
		totalConnectionIDs.Insert(activation.ResourceID)
	}

	if err = s.paymentProvider.ReportUsage(ctx, subscriptionItemID, int64(totalConnectionIDs.Count()), now); err != nil {
		return 0, err
	}

	return int64(totalConnectionIDs.Count()), nil
}

func (s *Service) reportMeteredUsage(
	ctx context.Context,
	exec database.Executor,
	aggregationType string,
	currentCycle model.DateRange,
	subscriptionItemID, instanceID string) (int64, error) {
	now := s.clock.Now().UTC()
	numResources, err := s.dailyAggregationsRepo.CountForInstanceAndRangeOnlyIncludedInUsage(
		ctx,
		exec,
		aggregationType,
		instanceID,
		currentCycle.Start,
		now)
	if err != nil {
		return 0, apierror.Unexpected(err)
	}
	err = s.paymentProvider.ReportUsage(
		ctx,
		subscriptionItemID,
		numResources,
		now,
	)
	if err != nil {
		return 0, err
	}

	return numResources, nil
}

func (s *Service) reportDomainsUsage(ctx context.Context, exec database.Executor, subscriptionItemID, instanceID string) (int64, error) {
	now := s.clock.Now().UTC()
	numberOfDomains, err := s.domainRepo.CountByInstanceID(ctx, exec, instanceID)
	if err != nil {
		return -1, err
	}

	satelliteDomains := numberOfDomains - 1
	if err = s.paymentProvider.ReportUsage(ctx, subscriptionItemID, satelliteDomains, now); err != nil {
		return -1, err
	}

	return numberOfDomains, nil
}
