package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"clerk/api/apierror"
	dapiserialize "clerk/api/dapi/serialize"
	"clerk/api/shared/pricing"
	"clerk/model"
	"clerk/model/sqbmodel"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/jobs"
	"clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/stripe/stripe-go/v72"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock                      clockwork.Clock
	db                         database.Database
	gueClient                  *gue.Client
	paymentProvider            clerkbilling.PaymentProvider
	billingService             *pricing.Service
	applicationRepo            *repository.Applications
	billingAccountRepo         *repository.BillingAccounts
	dailyAggregationRepo       *repository.DailyAggregations
	dailyUniqueActiveUsersRepo *repository.DailyUniqueActiveUsers
	instanceRepo               *repository.Instances
	organizationRepo           *repository.Organization
	subscriptionRepo           *repository.Subscriptions
	subscriptionMetricsRepo    *repository.SubscriptionMetrics
	subscriptionPlanRepo       *repository.SubscriptionPlans
	subscriptionPriceRepo      *repository.SubscriptionPrices
	subscriptionProductRepo    *repository.SubscriptionProduct
	userRepo                   *repository.Users
}

func NewService(deps clerk.Deps, paymentProvider clerkbilling.PaymentProvider) *Service {
	return &Service{
		clock:                      deps.Clock(),
		db:                         deps.DB(),
		gueClient:                  deps.GueClient(),
		paymentProvider:            clerkbilling.NewCachedPaymentProvider(deps.Clock(), deps.DB(), paymentProvider),
		billingService:             pricing.NewService(deps.DB(), deps.GueClient(), deps.Clock(), paymentProvider),
		billingAccountRepo:         repository.NewBillingAccounts(),
		applicationRepo:            repository.NewApplications(),
		dailyAggregationRepo:       repository.NewDailyAggregations(),
		dailyUniqueActiveUsersRepo: repository.NewDailyUniqueActiveUsers(),
		instanceRepo:               repository.NewInstances(),
		organizationRepo:           repository.NewOrganization(),
		subscriptionRepo:           repository.NewSubscriptions(),
		subscriptionPlanRepo:       repository.NewSubscriptionPlans(),
		subscriptionPriceRepo:      repository.NewSubscriptionPrices(),
		subscriptionProductRepo:    repository.NewSubscriptionProduct(),
		userRepo:                   repository.NewUsers(),
	}
}

func (s *Service) CurrentApplicationSubscription(
	ctx context.Context,
	exec database.Executor,
	appID string,
) (*dapiserialize.CurrentApplicationSubscriptionResponse, error) {
	application, err := s.applicationRepo.FindByID(ctx, exec, appID)
	if err != nil {
		return nil, err
	}

	subscription, err := s.subscriptionRepo.FindByResourceID(ctx, exec, appID)
	if err != nil {
		return nil, err
	}

	currentBillingCycle := subscription.GetCurrentBillingCycle(s.clock.Now().UTC())

	subscriptionPlans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, exec, subscription.ID)
	if err != nil {
		return nil, err
	}

	// create top-level plan with prices
	topLevelPlan := clerkbilling.DetectCurrentPlan(subscriptionPlans)
	if topLevelPlan == nil {
		return nil, errors.New("no top level subscription plan was found for " + appID)
	}
	topLevelPlanWithPrices, err := s.toSubscriptionPlanWithPrices(ctx, exec, topLevelPlan)
	if err != nil {
		return nil, err
	}

	// create addons with their prices
	addons, err := s.subscriptionPlanRepo.FindAllByIDs(ctx, exec, topLevelPlan.Addons)
	if err != nil {
		return nil, err
	}

	addons = orderAddonsAccordingToArray(topLevelPlan.Addons, addons)

	addonsWithPrices := make([]*model.SubscriptionPlanWithPrices, len(addons))
	for i, addon := range addons {
		addonsWithPrices[i], err = s.toSubscriptionPlanWithPrices(ctx, exec, addon)
		if err != nil {
			return nil, err
		}
	}

	var totalAmount int64
	var amountDue int64
	var creditBalance int64
	var invoiceDiscount *model.Discount
	var usages []*model.Usage
	if subscription.StripeSubscriptionID.Valid {
		invoice, err := s.paymentProvider.FetchNextInvoice(ctx, subscription.StripeSubscriptionID.String)
		if err != nil {
			return nil, err
		}
		totalAmount = invoice.Total
		amountDue = invoice.AmountDue
		creditBalance = invoice.Total - invoice.AmountDue
		invoiceDiscount = discountOf(invoice)

		usagesPerMetric, err := s.UsagesFromInvoice(ctx, exec, subscription, invoice)
		if err != nil {
			return nil, err
		}
		for _, usage := range usagesPerMetric {
			usages = append(usages, usage)
		}
	} else {
		// we are on a free plan
		usages, err = s.freeUsage(ctx, exec, appID, topLevelPlan, currentBillingCycle)
		if err != nil {
			return nil, err
		}
	}

	isInGracePeriod := len(subscription.GracePeriodFeatures) > 0

	var hasBillingAccount bool
	if subscription.BillingAccountID.Valid {
		billingAccount, err := s.billingAccountRepo.QueryByID(ctx, exec, subscription.BillingAccountID.String)
		if err != nil {
			return nil, err
		}
		hasBillingAccount = billingAccount != nil && billingAccount.StripeCustomerID.Valid
	}

	stripeUsageReport, err := repository.NewStripeUsageReports().QueryLatestByResourceID(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	lastUpdatedAt := s.clock.Now().UTC()
	if stripeUsageReport != nil {
		lastUpdatedAt = stripeUsageReport.SentAt
	}

	return dapiserialize.CurrentApplicationSubscription(
		appID,
		application.Name,
		subscription,
		topLevelPlanWithPrices,
		dapiserialize.WithSubscriptionAddons(addonsWithPrices, subscriptionPlans),
		dapiserialize.WithBillingCycle(currentBillingCycle),
		dapiserialize.WithUsage(totalAmount, amountDue, creditBalance, invoiceDiscount, lastUpdatedAt, usages),
		dapiserialize.CurrentSubscriptionGracePeriod(isInGracePeriod),
		dapiserialize.CurrentSubscriptionOrganizationMembershipLimit(
			model.MaxAllowedOrganizationMemberships(subscriptionPlans),
		),
		dapiserialize.CurrentSubscriptionHasBillingAccount(hasBillingAccount),
	), nil
}

func (s *Service) UsagesFromInvoice(
	ctx context.Context,
	exec database.Executor,
	subscription *model.Subscription,
	invoice *stripe.Invoice,
) (map[string]*model.Usage, error) {
	usages := make(map[string]*model.Usage)
	subscriptionMetrics, err := s.subscriptionMetricsRepo.FindAllBySubscriptionID(ctx, exec, subscription.ID)
	if err != nil {
		return nil, err
	}
	for _, subscriptionMetric := range subscriptionMetrics {
		if subscriptionMetric.Metric == "" || subscriptionMetric.Metric == clerkbilling.PriceTypes.Fixed {
			continue
		}
		usages[subscriptionMetric.Metric] = buildUsage(subscriptionMetric, invoice.Lines.Data)
	}
	return usages, nil
}

func (s *Service) toSubscriptionPlanWithPrices(ctx context.Context, exec database.Executor, subscriptionPlan *model.SubscriptionPlan) (*model.SubscriptionPlanWithPrices, error) {
	prices, err := s.subscriptionPriceRepo.FindAllByStripeProduct(ctx, exec, subscriptionPlan.StripeProductID.String)
	if err != nil {
		return nil, err
	}
	return model.NewSubscriptionPlanWithPrices(subscriptionPlan, prices), nil
}

func orderAddonsAccordingToArray(addonOrder []string, addons []*model.SubscriptionPlan) []*model.SubscriptionPlan {
	// order them according to the order of the array in the top-level plan
	addonsMap := make(map[string]*model.SubscriptionPlan, len(addons))
	for _, addon := range addons {
		addonsMap[addon.ID] = addon
	}

	orderedAddons := make([]*model.SubscriptionPlan, 0)
	for _, addonID := range addonOrder {
		addon := addonsMap[addonID]
		if addon == nil {
			continue
		}
		orderedAddons = append(orderedAddons, addon)
	}
	return orderedAddons
}

func discountOf(invoice *stripe.Invoice) *model.Discount {
	// no coupon discount
	if invoice.Discount == nil || invoice.Discount.Coupon == nil {
		return nil
	}
	// if there is coupon discount, check whether it applies to current subscription
	// by going over the total discount amounts and finding one that is not 0
	for _, discountAmount := range invoice.TotalDiscountAmounts {
		if discountAmount.Amount > 0 {
			return &model.Discount{
				PercentOff: invoice.Discount.Coupon.PercentOff,
				EndsAt:     time.Unix(invoice.Discount.End, 0),
			}
		}
	}
	return nil
}

func (s *Service) freeUsage(ctx context.Context, exec database.Executor, appID string, freePlan *model.SubscriptionPlan, billingCycle model.DateRange) ([]*model.Usage, error) {
	mauUsage := &model.Usage{
		Metric:    clerkbilling.PriceTypes.MAU,
		HardLimit: int64(freePlan.MonthlyUserLimit),
	}
	maoUsage := &model.Usage{
		Metric:    clerkbilling.PriceTypes.MAO,
		HardLimit: int64(freePlan.MonthlyOrganizationLimit),
	}
	usages := []*model.Usage{mauUsage, maoUsage}

	prodInstance, err := s.instanceRepo.QueryByApplicationAndEnvironmentType(ctx, exec, appID, constants.ETProduction)
	if err != nil {
		return nil, err
	}
	if prodInstance == nil {
		return usages, nil
	}

	// TODO(nikpolik): Remove once backfill of data is done
	if cenv.GetBool(cenv.FlagUseBillableCountForDailyActiveUsers) {
		mauUsage.TotalUnits, err = s.dailyUniqueActiveUsersRepo.CountBillableForInstanceAndRange(
			ctx, exec, prodInstance.ID, billingCycle.Start, billingCycle.End)
	} else {
		mauUsage.TotalUnits, err = s.dailyUniqueActiveUsersRepo.CountForInstanceAndRange(
			ctx, exec, prodInstance.ID, billingCycle.Start, billingCycle.End)
	}
	if err != nil {
		return nil, err
	}
	maoUsage.TotalUnits, err = s.dailyAggregationRepo.CountForInstanceAndRangeOnlyIncludedInUsage(
		ctx, exec, constants.OrganizationAggregationType, prodInstance.ID, billingCycle.Start, billingCycle.End)
	if err != nil {
		return nil, err
	}

	return usages, nil
}

func buildUsage(subscriptionMetric *model.SubscriptionMetric, lineItems []*stripe.InvoiceLine) *model.Usage {
	usage := &model.Usage{
		Metric: subscriptionMetric.Metric,
	}
	for _, lineItem := range lineItems {
		if lineItem.SubscriptionItem != subscriptionMetric.StripeSubscriptionItemID {
			continue
		}
		usage.AmountDue += lineItem.Amount
		usage.TotalUnits += lineItem.Quantity
		if usage.FreeLimit == 0 && lineItem.Price != nil && len(lineItem.Price.Tiers) > 0 {
			// find the free tier and assign it to the usage's free limit
			for _, tier := range lineItem.Price.Tiers {
				if tier.UnitAmount == 0 {
					usage.FreeLimit = tier.UpTo
					break
				}
			}
		}
	}
	return usage
}

type checkoutParams struct {
	Plans        []string `json:"plans"`
	resourceID   string   `json:"-"`
	resourceType string   `json:"-"`
	ownerID      string   `json:"-"`
	ownerType    string   `json:"-"`
}

func (s *Service) Checkout(ctx context.Context, params checkoutParams) (*dapiserialize.CheckoutResponse, apierror.Error) {
	billingAccount, apiErr := s.ensureBillingAccountForOwner(ctx, &pricing.Owner{
		ID:   params.ownerID,
		Type: params.ownerType,
	})
	if apiErr != nil {
		return nil, apiErr
	}

	plans, err := s.subscriptionPlanRepo.FindAllAvailableByIDsAndScope(ctx, s.db, params.resourceID, params.Plans, params.resourceType)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if len(plans) == 0 {
		return nil, apierror.ResourceNotFound()
	}
	prices, err := pricing.GetPricesForPlans(ctx, s.db, s.subscriptionPriceRepo, plans...)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var response *dapiserialize.CheckoutResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		clerkSubscription, err := s.subscriptionRepo.FindByResourceIDForUpdate(ctx, tx, params.resourceID)
		if err != nil {
			return true, err
		}

		// Switching to a non paid plan, return early.
		if len(prices) == 0 {
			err = s.switchToNonPaid(ctx, tx, clerkSubscription, plans...)
			if err != nil {
				return true, err
			}
			response = dapiserialize.CheckoutNonPaid(clerkSubscription)
			return false, nil
		}

		stripeSubscription, err := s.initiateNewSubscription(
			ctx,
			tx,
			clerkSubscription,
			prices,
			billingAccount,
			params.resourceID,
		)
		if err != nil {
			return true, err
		}

		response = dapiserialize.Checkout(clerkSubscription, stripeSubscription, plans, prices)
		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) ensureBillingAccountForOwner(
	ctx context.Context,
	owner *pricing.Owner,
) (*model.BillingAccount, apierror.Error) {
	var billingAccount *model.BillingAccount
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		billingAccount, err = s.billingService.EnsureBillingAccountForOwner(ctx, tx, owner)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return billingAccount, nil
}

func (s *Service) switchToNonPaid(
	ctx context.Context,
	tx database.Tx,
	subscription *model.Subscription,
	plans ...*model.SubscriptionPlan,
) error {
	previousSubscriptionID := subscription.StripeSubscriptionID.String

	err := s.billingService.ClearPaidSubscriptionColumns(ctx, tx, subscription)
	if err != nil {
		return err
	}

	billableResource, err := s.billingService.BillableResourceForSubscription(ctx, tx, subscription)
	if err != nil {
		return err
	}
	err = s.billingService.SwitchResourceToPlan(ctx, tx, billableResource, subscription, plans...)
	if err != nil {
		return err
	}

	if previousSubscriptionID != "" {
		err := s.paymentProvider.CancelSubscription(ctx, previousSubscriptionID, clerkbilling.CancelSubscriptionParams{
			InvoiceNow: true,
			Prorate:    true,
		})
		if err != nil {
			sentry.CaptureException(ctx, fmt.Errorf("subscriptions/switchToNonPaid: failed to cancel subscription %s on payment provider: %w", previousSubscriptionID, err))
		}
	}

	return nil
}

func (s *Service) initiateNewSubscription(
	ctx context.Context,
	tx database.Tx,
	clerkSubscription *model.Subscription,
	prices []*model.SubscriptionPrice,
	billingAccount *model.BillingAccount,
	resourceID string,
) (*stripe.Subscription, error) {
	if clerkSubscription.IncompleteStripeSubscriptionID.Valid {
		// ignore error, no need to do anything anyway, given that these are temporary
		// subscriptions
		_ = s.paymentProvider.CancelSubscription(ctx, clerkSubscription.IncompleteStripeSubscriptionID.String, clerkbilling.CancelSubscriptionParams{
			InvoiceNow: false,
			Prorate:    false,
		})
	}

	stripeSubscription, err := s.paymentProvider.CreateSubscription(clerkbilling.CreateSubscriptionParams{
		CustomerID:        billingAccount.StripeCustomerID.Ptr(),
		ResourceID:        resourceID,
		Prices:            prices,
		TrialPeriodDays:   clerkSubscription.TrialPeriodDays,
		DefaultIncomplete: true,
	})
	if err != nil {
		return nil, err
	}

	clerkSubscription.ResetPaymentStatus()
	clerkSubscription.IncompleteStripeSubscriptionID = null.StringFrom(stripeSubscription.ID)
	clerkSubscription.BillingAccountID = null.StringFrom(billingAccount.ID)
	err = s.subscriptionRepo.Update(
		ctx,
		tx,
		clerkSubscription,
		sqbmodel.SubscriptionColumns.PaymentStatus,
		sqbmodel.SubscriptionColumns.IncompleteStripeSubscriptionID,
		sqbmodel.SubscriptionColumns.BillingAccountID,
	)
	if err != nil {
		return nil, err
	}

	cleanupIncompleteStripeSubscriptionAfter := cenv.GetDurationInSeconds(cenv.CleanupIncompleteSubscriptionsAfterSeconds)
	fmt.Println(cleanupIncompleteStripeSubscriptionAfter)
	fmt.Println(cenv.Get(cenv.CleanupIncompleteSubscriptionsAfterSeconds))
	if cleanupIncompleteStripeSubscriptionAfter > 0 {
		runAt := s.clock.Now().UTC().Add(cleanupIncompleteStripeSubscriptionAfter)
		err = jobs.CleanupIncompleteStripeSubscription(ctx, s.gueClient, jobs.CleanupIncompleteStripeSubscriptionArgs{
			ClerkSubscriptionID:  clerkSubscription.ID,
			StripeSubscriptionID: stripeSubscription.ID,
		}, jobs.WithTx(tx), jobs.WithRunAt(&runAt))
		if err != nil {
			return nil, err
		}
	}
	return stripeSubscription, nil
}

type completeParams struct {
	resourceID string `json:"-"`
}

type CompleteResponse struct {
	SubscriptionID string `json:"subscription_id"`
	PaymentStatus  string `json:"payment_status"`
}

// ApplicationComplete is a callback for successful application checkout flows.
// Updates the application subscription payment status based on the
// latest state on the payment provider side.
func (s *Service) ApplicationComplete(ctx context.Context, params completeParams) (*CompleteResponse, apierror.Error) {
	var subscription *model.Subscription
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		subscription, err = s.complete(ctx, tx, params)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return &CompleteResponse{
		SubscriptionID: subscription.ID,
		PaymentStatus:  subscription.PaymentStatus,
	}, nil
}

// Completes the checkout flow for a subscription.
func (s *Service) complete(
	ctx context.Context,
	tx database.Tx,
	params completeParams,
) (*model.Subscription, apierror.Error) {
	clerkSubscription, err := s.subscriptionRepo.FindByResourceIDForUpdate(ctx, tx, params.resourceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if !clerkSubscription.IncompleteStripeSubscriptionID.Valid {
		return nil, apierror.ResourceNotFound()
	}
	previousStripeSubscriptionID := clerkSubscription.StripeSubscriptionID.Ptr()

	stripeSubscription, apiErr := s.applyStripeSubscription(ctx, tx, clerkSubscription)
	if apiErr != nil {
		return nil, apiErr
	}

	priceIDs := set.New[string]()
	for _, subscriptionItem := range stripeSubscription.Items.Data {
		priceIDs.Insert(subscriptionItem.Price.ID)
	}
	plans, err := s.subscriptionPlanRepo.FindAvailableByStripePriceIDs(ctx, tx, params.resourceID, priceIDs.Array())
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	err = s.switchToPlan(ctx, tx, clerkSubscription, previousStripeSubscriptionID, plans...)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return clerkSubscription, nil
}

func (s *Service) applyStripeSubscription(
	ctx context.Context,
	tx database.Tx,
	clerkSubscription *model.Subscription,
) (*stripe.Subscription, apierror.Error) {
	stripeSubscription, err := s.paymentProvider.FetchSubscription(clerkSubscription.IncompleteStripeSubscriptionID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if stripeSubscription.Status != stripe.SubscriptionStatusActive && stripeSubscription.Status != stripe.SubscriptionStatusTrialing {
		return nil, apierror.InactiveSubscription(clerkSubscription.ID)
	}

	apiErr := s.billingService.ApplyStripeSubscription(ctx, tx, clerkSubscription, stripeSubscription)
	if apiErr != nil {
		return nil, apiErr
	}
	clerkSubscription.CompletePaymentStatus()
	clerkSubscription.IncompleteStripeSubscriptionID = null.StringFromPtr(nil)
	err = s.subscriptionRepo.UpdateIncompleteStripeSubscriptionAndPaymentStatus(ctx, tx, clerkSubscription)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return stripeSubscription, nil
}

func (s *Service) switchToPlan(
	ctx context.Context,
	tx database.Tx,
	clerkSubscription *model.Subscription,
	previousStripeSubscriptionID *string,
	plans ...*model.SubscriptionPlan,
) error {
	billableResource, err := s.billingService.BillableResourceForSubscription(ctx, tx, clerkSubscription)
	if err != nil {
		return err
	}
	err = s.billingService.SwitchResourceToPlan(ctx, tx, billableResource, clerkSubscription, plans...)
	if err != nil {
		return err
	}

	if previousStripeSubscriptionID != nil {
		err := s.paymentProvider.CancelSubscription(ctx, *previousStripeSubscriptionID, clerkbilling.CancelSubscriptionParams{
			InvoiceNow: true,
			Prorate:    true,
		})
		if err != nil {
			sentry.CaptureException(ctx, fmt.Errorf("subscriptions/switchToPlan: failed to cancel subscription %s on payment provider: %w", *previousStripeSubscriptionID, err))
		}
	}

	return nil
}

// OrganizationComplete is a callback for successful organization
// checkout flows.
// Updates the organization subscription payment status and sets
// the organization max allowed memberships according to the
// successfully checked out plans.
func (s *Service) OrganizationComplete(ctx context.Context, params completeParams) (*CompleteResponse, apierror.Error) {
	organization, err := s.organizationRepo.QueryByID(ctx, s.db, params.resourceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if organization == nil {
		return nil, apierror.OrganizationNotFound()
	}

	var subscription *model.Subscription
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		subscription, err = s.complete(ctx, tx, params)
		if err != nil {
			return true, err
		}

		plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, subscription.ID)
		if err != nil {
			return true, err
		}
		organization.MaxAllowedMemberships = model.MaxAllowedOrganizationMemberships(plans)
		err = s.organizationRepo.UpdateMaxAllowedMemberships(ctx, tx, organization)
		if err != nil {
			return true, err
		}

		return false, err
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return &CompleteResponse{
		SubscriptionID: subscription.ID,
		PaymentStatus:  subscription.PaymentStatus,
	}, nil
}
