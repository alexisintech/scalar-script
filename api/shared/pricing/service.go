package pricing

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/environment"
	"clerk/api/shared/features"
	"clerk/model"
	"clerk/model/sqbmodel"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/segment"
	"clerk/pkg/externalapis/slack"
	"clerk/pkg/jobs"
	"clerk/pkg/notifications"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/stripe/stripe-go/v72"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock           clockwork.Clock
	gueClient       *gue.Client
	paymentProvider clerkbilling.PaymentProvider

	environmentService *environment.Service
	featureService     *features.Service

	// repositories
	applicationRepo         *repository.Applications
	billingRepo             *repository.BillingAccounts
	instanceRepo            *repository.Instances
	organizationRepo        *repository.Organization
	pricesRepo              *repository.SubscriptionPrices
	subscriptionsRepo       *repository.Subscriptions
	subscriptionMetricsRepo *repository.SubscriptionMetrics
	subscriptionPlansRepo   *repository.SubscriptionPlans
	subscriptionPricesRepo  *repository.SubscriptionPrices
	subscriptionProductRepo *repository.SubscriptionProduct
	userRepo                *repository.Users
}

func NewService(db database.Database, gueClient *gue.Client, clock clockwork.Clock, paymentProvider clerkbilling.PaymentProvider) *Service {
	return &Service{
		clock:                   clock,
		gueClient:               gueClient,
		paymentProvider:         paymentProvider,
		featureService:          features.NewService(db, gueClient),
		environmentService:      environment.NewService(),
		applicationRepo:         repository.NewApplications(),
		billingRepo:             repository.NewBillingAccounts(),
		instanceRepo:            repository.NewInstances(),
		organizationRepo:        repository.NewOrganization(),
		pricesRepo:              repository.NewSubscriptionPrices(),
		subscriptionsRepo:       repository.NewSubscriptions(),
		subscriptionMetricsRepo: repository.NewSubscriptionMetrics(),
		subscriptionPlansRepo:   repository.NewSubscriptionPlans(),
		subscriptionPricesRepo:  repository.NewSubscriptionPrices(),
		subscriptionProductRepo: repository.NewSubscriptionProduct(),
		userRepo:                repository.NewUsers(),
	}
}

var NoUnsupportedFeatures = func(...*model.SubscriptionPlan) ([]string, error) {
	return []string{}, nil
}

type BillableResource struct {
	ID                  string
	Name                string
	Type                string
	UnsupportedFeatures func(...*model.SubscriptionPlan) ([]string, error)
}

type CreateCheckoutSessionParams struct {
	NewPlanIDs []string
	Owner      *Owner
	Resource   BillableResource

	ReturnURL *url.URL
	SessionID string
	Origin    string
}

// CreateCheckoutSession prepares a Stripe Checkout Session and returns the URL
// for it.
func (s *Service) CreateCheckoutSession(ctx context.Context, tx database.Tx, params CreateCheckoutSessionParams) (string, apierror.Error) {
	newPlans, err := s.subscriptionPlansRepo.FindAllAvailableByIDsAndScope(ctx, tx, params.Resource.ID, params.NewPlanIDs, params.Resource.Type)
	if err != nil {
		return "", apierror.Unexpected(err)
	}
	if len(newPlans) == 0 {
		return "", apierror.ResourceNotFound()
	}

	subscription, err := s.subscriptionsRepo.FindByResourceIDForUpdate(ctx, tx, params.Resource.ID)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	// Disallow if another payment for this application is pending
	if subscription.PaymentStatus == constants.PaymentStatusPending {
		return "", apierror.CheckoutLocked(params.Resource.ID)
	}

	subscriptionPlans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, tx, subscription.ID)
	if err != nil {
		return "", apierror.Unexpected(err)
	}
	newPlansSet := set.New(params.NewPlanIDs...)
	for _, subscriptionPlan := range subscriptionPlans {
		if newPlansSet.Contains(subscriptionPlan.ID) {
			return "", apierror.InvalidPlanForResource(params.Resource.ID, params.Resource.Type, subscriptionPlan.ID)
		}
	}

	billingAcct, apiErr := s.EnsureBillingAccountForOwner(ctx, tx, params.Owner)
	if apiErr != nil {
		return "", apiErr
	}

	subscriptionUpdateCols := make([]string, 0)

	if !subscription.BillingAccountID.Valid {
		subscription.BillingAccountID = null.StringFrom(billingAcct.ID)
		subscriptionUpdateCols = append(subscriptionUpdateCols, sqbmodel.SubscriptionColumns.BillingAccountID)
	}

	allStripeProductIDs := set.New[string]()
	for _, newPlan := range newPlans {
		if newPlan.StripeProductID.Valid {
			allStripeProductIDs.Insert(newPlan.StripeProductID.String)
		}
	}
	prices, err := s.pricesRepo.FindAllByStripeProduct(ctx, tx, allStripeProductIDs.Array()...)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	existingSubID := subscription.StripeSubscriptionID.String
	if len(prices) == 0 {
		if existingSubID != "" {
			err = s.paymentProvider.CancelSubscription(ctx, existingSubID, clerkbilling.CancelSubscriptionParams{
				InvoiceNow: true,
				Prorate:    false,
			})
			if err != nil {
				return "", apierror.Unexpected(err)
			}
		}

		err := s.ClearPaidSubscriptionColumns(ctx, tx, subscription)
		if err != nil {
			return "", apierror.Unexpected(err)
		}

		// find the first non-addon plan
		nonAddonPlan := clerkbilling.DetectCurrentPlan(newPlans)
		err = s.SwitchResourceToPlan(ctx, tx, params.Resource, subscription, nonAddonPlan)
		if err != nil {
			return "", apierror.Unexpected(err)
		}

		return dashboardReturnURL(params.Resource.ID, params.SessionID, params.Origin, params.ReturnURL), nil
	}

	var checkoutSessionID string
	responseURL, checkoutSessionID, err := s.paymentProvider.CheckoutURL(
		clerkbilling.CheckoutParams{
			SuccessURL:      dashboardSuccessURL(params.Resource.ID, params.SessionID, params.Origin, params.ReturnURL),
			CancelURL:       dashboardCancelURL(params.Resource.ID, params.SessionID, params.Origin, params.ReturnURL),
			ResourceID:      params.Resource.ID,
			CustomerID:      billingAcct.StripeCustomerID.Ptr(),
			Prices:          prices,
			TrialPeriodDays: subscription.TrialPeriodDays,
		})
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	subscription.StripeCheckoutSessionID = null.StringFrom(checkoutSessionID)
	subscription.PaymentStatus = constants.PaymentStatusInitiated
	subscriptionUpdateCols = append(subscriptionUpdateCols,
		sqbmodel.SubscriptionColumns.StripeCheckoutSessionID,
		sqbmodel.SubscriptionColumns.PaymentStatus)

	err = s.subscriptionsRepo.Update(ctx, tx, subscription, subscriptionUpdateCols...)
	if err != nil {
		return "", apierror.Unexpected(err)
	}

	return responseURL, nil
}

type CheckoutSessionCompletedParams struct {
	CheckoutSession stripe.CheckoutSession
	Resource        BillableResource
	Subscription    *model.Subscription
}

// CheckoutSessionCompleted handles the webhook after the Stripe Session is completed.
func (s *Service) CheckoutSessionCompleted(ctx context.Context, tx database.Tx, params CheckoutSessionCompletedParams) apierror.Error {
	if !params.Subscription.StripeCheckoutSessionID.Valid ||
		params.Subscription.StripeCheckoutSessionID.String == "" ||
		params.CheckoutSession.ID != params.Subscription.StripeCheckoutSessionID.String {
		apiErr := apierror.CheckoutSessionMismatch(params.Resource.ID, params.CheckoutSession.ID)
		sentryclerk.CaptureException(ctx, clerkerrors.WithStacktrace("billing/checkoutSessionCompleted failed: %s", apiErr.Error()))
		return apiErr
	}

	customerID := params.CheckoutSession.Customer.ID

	if err := s.setStripeCustomerID(ctx, tx, params.Subscription, customerID); err != nil {
		return apierror.Unexpected(err)
	}

	// "FSM"
	// state = app.PaymentStatus
	// event = checkoutSession.PaymentStatus

	switch params.Subscription.PaymentStatus {
	case constants.PaymentStatusInitiated:
		switch params.CheckoutSession.PaymentStatus {
		case stripe.CheckoutSessionPaymentStatusPaid:
			// Mark as paid & continue
			params.Subscription.PaymentStatus = constants.PaymentStatusComplete
		case stripe.CheckoutSessionPaymentStatusUnpaid:
			// Mark as pending & skip further processing
			params.Subscription.PaymentStatus = constants.PaymentStatusPending
			if err := s.subscriptionsRepo.UpdatePaymentStatus(ctx, tx, params.Subscription); err != nil {
				return apierror.Unexpected(err)
			}
			return nil
		default:
			// stripe.CheckoutSessionPaymentStatusUnpaid not expected
			return nil
		}
	case constants.PaymentStatusPending:
		switch params.CheckoutSession.PaymentStatus {
		case stripe.CheckoutSessionPaymentStatusPaid:
			// Mark as paid & continue
			params.Subscription.PaymentStatus = constants.PaymentStatusComplete
		case stripe.CheckoutSessionPaymentStatusUnpaid:
			// Stay in pending & skip further processing
			return nil
		default:
			// stripe.CheckoutSessionPaymentStatusUnpaid not expected
			return nil
		}
	case constants.PaymentStatusComplete:
		// Either already processed or incompatible with current state - ignore
		return nil
	}

	err := s.subscriptionsRepo.UpdatePaymentStatus(ctx, tx, params.Subscription)
	if err != nil {
		return apierror.Unexpected(err)
	}

	// The webhook payload only returns the ID of the subscription, we need to
	// request the full object from the APIobject.
	stripeSubscription, err := s.paymentProvider.FetchSubscription(params.CheckoutSession.Subscription.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if stripeSubscription.Status != stripe.SubscriptionStatusActive && stripeSubscription.Status != stripe.SubscriptionStatusTrialing {
		return apierror.Unexpected(fmt.Errorf("subscription %s is not active or trialing, found %s instead",
			stripeSubscription.ID, stripeSubscription.Status))
	}

	oldStripeSubscriptionID := params.Subscription.StripeSubscriptionID

	apiErr := s.ApplyStripeSubscription(ctx, tx, params.Subscription, stripeSubscription)
	if apiErr != nil {
		return apiErr
	}

	// Cancel old subscription if we were already on a paid plan, to avoid double-charging
	if oldStripeSubscriptionID.Valid {
		err = s.paymentProvider.CancelSubscription(ctx, oldStripeSubscriptionID.String, clerkbilling.CancelSubscriptionParams{
			InvoiceNow: true,
			Prorate:    true,
		})
		if err != nil {
			sentryclerk.CaptureException(ctx, fmt.Errorf("failed to cancel subscription %s: %w", oldStripeSubscriptionID.String, err))
		}
	}

	prices := set.New[string]()
	for _, subscriptionItem := range stripeSubscription.Items.Data {
		prices.Insert(subscriptionItem.Price.ID)
	}

	plans, err := s.subscriptionPlansRepo.FindAvailableByStripePriceIDs(ctx, tx, params.Resource.ID, prices.Array())
	if err != nil {
		return apierror.Unexpected(err)
	}

	if err := s.SwitchResourceToPlan(ctx, tx, params.Resource, params.Subscription, plans...); err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

// ApplyStripeSubscription updates our local DB copy of a subscription with the status
// of the given Stripe subscription.
func (s *Service) ApplyStripeSubscription(
	ctx context.Context,
	exec database.Executor,
	subscription *model.Subscription,
	stripeSubscription *stripe.Subscription) apierror.Error {
	subscriptionUpdateCols := make([]string, 0)

	subscription.BillingCycleAnchor = null.TimeFrom(
		time.Unix(stripeSubscription.BillingCycleAnchor, 0).UTC(),
	)
	subscriptionUpdateCols = append(subscriptionUpdateCols, sqbmodel.SubscriptionColumns.BillingCycleAnchor)

	// if no subscription items, no need to do anything further
	if len(stripeSubscription.Items.Data) == 0 {
		return nil
	}

	subscription.StripeSubscriptionID = null.StringFrom(stripeSubscription.ID)
	subscription.TrialPeriodDays = 0
	subscriptionUpdateCols = append(subscriptionUpdateCols,
		sqbmodel.SubscriptionColumns.StripeSubscriptionID,
		sqbmodel.SubscriptionColumns.TrialPeriodDays)

	if err := s.subscriptionsRepo.Update(ctx, exec, subscription, subscriptionUpdateCols...); err != nil {
		return apierror.Unexpected(err)
	}

	// remove all subscription metrics to start with clean slate
	if err := s.subscriptionMetricsRepo.DeleteBySubscriptionID(ctx, exec, subscription.ID); err != nil {
		return apierror.Unexpected(err)
	}

	// iterate through all prices and populate the necessary columns in the application
	for _, item := range stripeSubscription.Items.Data {
		var metric string
		if item.Price.Metadata != nil {
			metric = item.Price.Metadata["metric"]
		}

		var subscriptionMetric *model.SubscriptionMetric
		if metric != "" && metric != clerkbilling.PriceTypes.Fixed {
			subscriptionMetric = model.NewSubscriptionMetric(subscription.ID, metric, item.ID)
		} else if len(stripeSubscription.Items.Data) == 1 {
			// this case is here to support older plans that had a single price
			// which would include both the package price and metered usage for MAUs
			subscriptionMetric = model.NewSubscriptionMetric(subscription.ID, clerkbilling.PriceTypes.MAU, stripeSubscription.Items.Data[0].ID)
		} else {
			// not a metered price used for a particular metric, so skipping...
			continue
		}

		if err := s.subscriptionMetricsRepo.Insert(ctx, exec, subscriptionMetric); err != nil {
			return apierror.Unexpected(err)
		}
	}
	return nil
}

// CreateMissingSubscriptionMetrics creates one subscription metric for each Stripe
// subscription item, unless the metric already exists.
func (s *Service) CreateMissingSubscriptionMetrics(
	ctx context.Context,
	tx database.Tx,
	stripeSubscriptionItems []*stripe.SubscriptionItem,
	clerkSubscriptionID string,
	existingMetrics []*model.SubscriptionMetric,
) error {
	existingMetricsByID := set.New[string]()
	for _, metric := range existingMetrics {
		existingMetricsByID.Insert(metric.StripeSubscriptionItemID)
	}

	for _, item := range stripeSubscriptionItems {
		if existingMetricsByID.Contains(item.ID) {
			continue
		}
		metric, ok := item.Price.Metadata["metric"]
		if !ok || metric == "" || metric == clerkbilling.PriceTypes.Fixed {
			continue
		}
		err := s.subscriptionMetricsRepo.Insert(ctx, tx, model.NewSubscriptionMetric(clerkSubscriptionID, metric, item.ID))
		if err != nil {
			return err
		}
	}
	return nil
}

// SwitchResourceToPlan switches the passed resource to the passed plan
func (s *Service) SwitchResourceToPlan(
	ctx context.Context,
	tx database.Tx,
	resource BillableResource,
	subscription *model.Subscription,
	newPlans ...*model.SubscriptionPlan,
) error {
	currentPlans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, tx, subscription.ID)
	if err != nil {
		return err
	}
	err = s.monitorFreeMAUUsage(ctx, tx, resource, currentPlans)
	if err != nil {
		return err
	}

	s.notifyPlanSwitch(ctx, tx, resource, currentPlans, newPlans...)
	err = s.trackPlanSwitch(ctx, resource.ID, currentPlans, newPlans...)
	if err != nil {
		return err
	}

	_, err = s.subscriptionProductRepo.DeleteBySubscriptionID(ctx, tx, subscription.ID)
	if err != nil {
		return err
	}

	// Check if all features are supported by the new plan,
	// otherwise put the resource in grace period.
	err = s.HandleGracePeriod(ctx, tx, resource, subscription, currentPlans, newPlans...)
	if err != nil {
		return err
	}

	for _, newPlan := range newPlans {
		err = s.subscriptionProductRepo.Insert(ctx, tx, model.NewSubscriptionProduct(subscription.ID, newPlan.ID))
		if err != nil {
			return err
		}
	}

	return nil
}

// Send an alert about switching from currentPlans to newPlans.
func (s *Service) notifyPlanSwitch(
	ctx context.Context,
	tx database.Tx,
	resource BillableResource,
	currentPlans []*model.SubscriptionPlan,
	newPlans ...*model.SubscriptionPlan,
) {
	upgrade, downgrade := false, false

	currentPlanTitles := make([]string, len(currentPlans))
	for i, p := range currentPlans {
		if strings.HasPrefix(p.ID, "free_") {
			upgrade = true
		}
		currentPlanTitles[i] = p.Title
	}
	newPlanTitles := make([]string, len(newPlans))
	for i, p := range newPlans {
		if strings.HasPrefix(p.ID, "free_") {
			downgrade = true
		}
		newPlanTitles[i] = p.Title
	}

	emoji := "↔️"
	if upgrade {
		emoji = "🎉"
	} else if downgrade {
		emoji = "😔"
	}

	err := jobs.SendSlackAlert(
		ctx,
		s.gueClient,
		jobs.SlackAlertArgs{
			Webhook: constants.SlackBilling,
			Message: slack.Message{
				Title: fmt.Sprintf("%s Switching plans", emoji),
				Text: fmt.Sprintf(
					"Application '%s' (%s) switches from %s to %s",
					resource.Name,
					resource.ID,
					currentPlanTitles,
					newPlanTitles,
				),
				Type: slack.Info,
			},
		},
		jobs.WithTx(tx),
	)
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}
}

func (s *Service) trackPlanSwitch(
	ctx context.Context,
	resourceID string,
	previousPlans []*model.SubscriptionPlan,
	newPlans ...*model.SubscriptionPlan,
) error {
	// Out of all the previous and new plans, only one is an actual
	// plan and the rest are addons.
	previousPlan := clerkbilling.DetectCurrentPlan(previousPlans)
	newPlan := clerkbilling.DetectCurrentPlan(newPlans)
	if previousPlan == nil || newPlan == nil {
		return fmt.Errorf("cannot detect previous or new plan for resource %s", resourceID)
	}

	// Generic catch-all for plan change
	eventName := segment.APIBackendBillingPlanChanged
	// If coming to free plan, or if downgrading from business to hobby, send downgrade event
	if newPlan.Title == "Free" || (newPlan.Title == "Hobby" && previousPlan.Title == "Business") {
		eventName = segment.APIBackendBillingPlanDowngraded
		// If going from free from to ANY plan or upgrading from hobby to business, send upgrade event
	} else if (previousPlan.Title == "Free" && newPlan.Title != "Free") ||
		(previousPlan.Title == "Hobby" && newPlan.Title == "Business") {
		eventName = segment.APIBackendBillingPlanUpgraded
	}

	err := jobs.SegmentEnqueueEvent(ctx, s.gueClient, jobs.SegmentArgs{
		Event:         eventName,
		ApplicationID: resourceID,
		Properties: map[string]any{
			"surface":        "API",
			"location":       "Dashboard",
			"applicationId":  resourceID,
			"newPlan":        newPlan.Title,
			"newPlanID":      newPlan.ID,
			"previousPlan":   previousPlan.Title,
			"previousPlanID": previousPlan.ID,
		},
	})
	if err != nil {
		sentryclerk.CaptureException(ctx, fmt.Errorf("enqueue segment job: %w", err))
	}

	// Track any addon changes
	for _, previousAddon := range previousPlans {
		if !previousAddon.IsAddon {
			continue
		}
		err := jobs.SegmentEnqueueEvent(ctx, s.gueClient, jobs.SegmentArgs{
			Event:         segment.APIBackendBillingPlanAddOnDisabled,
			ApplicationID: resourceID,
			Properties: map[string]any{
				"surface":       "API",
				"location":      "Dashboard",
				"applicationId": resourceID,
				"productTitle":  previousAddon.Title,
				"productId":     previousAddon.ID,
			},
		})
		if err != nil {
			sentryclerk.CaptureException(ctx, fmt.Errorf("enqueue segment job: %w", err))
		}
	}

	for _, newAddon := range newPlans {
		if !newAddon.IsAddon {
			continue
		}
		err := jobs.SegmentEnqueueEvent(ctx, s.gueClient, jobs.SegmentArgs{
			Event:         segment.APIBackendBillingPlanAddOnEnabled,
			ApplicationID: resourceID,
			Properties: map[string]any{
				"surface":       "API",
				"location":      "Dashboard",
				"applicationId": resourceID,
				"productTitle":  newAddon.Title,
				"productId":     newAddon.ID,
			},
		})
		if err != nil {
			sentryclerk.CaptureException(ctx, fmt.Errorf("enqueue segment job: %w", err))
		}
	}

	return nil
}

func (s *Service) monitorFreeMAUUsage(
	ctx context.Context,
	tx database.Tx,
	resource BillableResource,
	plans []*model.SubscriptionPlan,
) error {
	if resource.Type != constants.ApplicationResource {
		return nil
	}
	prodInstance, err := s.instanceRepo.QueryByApplicationAndEnvironmentType(ctx, tx, resource.ID, constants.ETProduction)
	if err != nil {
		return err
	}
	if prodInstance == nil {
		return nil
	}

	productIDs := set.New[string]()
	for _, plan := range plans {
		productIDs.Insert(plan.StripeProductID.String)
	}
	prices, err := s.subscriptionPricesRepo.FindAllByStripeProduct(ctx, tx, productIDs.Array()...)
	if err != nil {
		return err
	}
	if len(prices) != 0 {
		return nil
	}
	args := jobs.CheckFreeMAULimitArgs{
		InstanceID: prodInstance.ID,
	}
	if err := jobs.CheckFreeMAULimit(ctx, s.gueClient, args, jobs.WithTx(tx)); err != nil {
		sentryclerk.CaptureException(ctx, fmt.Errorf("cannot schedule free mau usage job for instance %s: %w", prodInstance.ID, err))
	}
	return nil
}

func (s *Service) HandleGracePeriod(
	ctx context.Context,
	tx database.Tx,
	resource BillableResource,
	subscription *model.Subscription,
	currentPlans []*model.SubscriptionPlan,
	newPlans ...*model.SubscriptionPlan,
) error {
	previousUnsupportedFeatures := subscription.GracePeriodFeatures
	subscription.GracePeriodFeatures = []string{}
	if err := s.subscriptionsRepo.UpdateGracePeriodFeatures(ctx, tx, subscription); err != nil {
		return err
	}

	unsupportedFeatures, err := resource.UnsupportedFeatures(newPlans...)
	if err != nil {
		return err
	}

	subscription.GracePeriodFeatures = unsupportedFeatures

	newUnsupportedFeatures := set.New[string](unsupportedFeatures...)
	newUnsupportedFeatures.Remove(previousUnsupportedFeatures...)
	if newUnsupportedFeatures.Count() > 0 {
		subscription.EnteredGracePeriodAt = null.TimeFrom(s.clock.Now().UTC())

		offenders := make([]*model.SubscriptionPlan, 0)
		for _, plan := range currentPlans {
			if set.New[string](plan.Features...).IsOverlapping(newUnsupportedFeatures) {
				offenders = append(offenders, plan)
			}
		}

		s.NotifyGracePeriod(ctx, tx, resource.ID, resource.Name, unsupportedFeatures, offenders...)
		s.ScheduleRemoveGracePeriod(ctx, tx, subscription.ResourceID)
	} else if len(unsupportedFeatures) == 0 {
		subscription.EnteredGracePeriodAt = null.TimeFromPtr(nil)
	}
	return s.subscriptionsRepo.UpdateGracePeriodFeaturesAndTime(ctx, tx, subscription)
}

// ScheduleRemoveGracePeriod enqueues a job to remove the product from grace
// period and disable its features.
func (s *Service) ScheduleRemoveGracePeriod(ctx context.Context, tx database.Tx, applicationID string) {
	gracePeriodEnd := s.clock.Now().Add(time.Duration(cenv.GetInt(cenv.GracePeriodDurationSeconds)) * time.Second)
	err := jobs.RemoveFromGracePeriod(
		ctx,
		s.gueClient,
		jobs.RemoveFromGracePeriodArgs{
			ApplicationID: applicationID,
		},
		jobs.WithTx(tx),
		jobs.WithRunAt(&gracePeriodEnd),
	)
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}
}

// NotifyGracePeriod sends a slack alert about the resource entering
// grace period.
func (s *Service) NotifyGracePeriod(
	ctx context.Context,
	tx database.Tx,
	resourceID,
	resourceName string,
	unsupportedFeatures []string,
	products ...*model.SubscriptionPlan,
) {
	productTitles := make([]string, len(products))
	productIDs := make([]string, len(products))
	for i, product := range products {
		productTitles[i] = product.Title
		productIDs[i] = product.ID
	}

	// ensure consistent ordering of unsupported features
	slices.Sort(unsupportedFeatures)

	err := jobs.SendSlackAlert(ctx, s.gueClient, jobs.SlackAlertArgs{
		Webhook: constants.SlackBilling,
		Message: slack.Message{
			Title: "Grace period for product",
			Text: notifications.EnterGracePeriodChatMessage(
				resourceName,
				resourceID,
				productTitles,
				productIDs,
				unsupportedFeatures,
			),
			Type: slack.Error,
		},
	},
		jobs.WithTx(tx),
	)
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}
}

type CreateCustomerPortalSessionParams struct {
	Owner      *Owner
	ResourceID string
	ReturnURL  *url.URL
	SessionID  string
	Origin     string
}

// CreateCustomerPortalSession prepares a Stripe Customer Portal Session and
// returns the URL for it.
func (s *Service) CreateCustomerPortalSession(ctx context.Context, tx database.Tx, params CreateCustomerPortalSessionParams) (string, apierror.Error) {
	billingAccount, apiErr := s.EnsureBillingAccountForOwner(ctx, tx, params.Owner)
	if apiErr != nil {
		return "", apiErr
	}

	customerPortalParams := &stripe.BillingPortalSessionParams{
		Customer:  billingAccount.StripeCustomerID.Ptr(),
		ReturnURL: stripe.String(dashboardReturnURL(params.ResourceID, params.SessionID, params.Origin, params.ReturnURL)),
	}
	responseURL, err := s.paymentProvider.CustomerPortalURL(cenv.Get(cenv.StripeSecretKey), customerPortalParams)
	if err != nil {
		return "", apierror.Unexpected(err)
	}
	return responseURL, nil
}

// setStripeCustomerID sets the StripeCustomerID of the billing account linked
// to the passed app, unless it is already set.
func (s *Service) setStripeCustomerID(ctx context.Context, tx database.Tx, subscription *model.Subscription,
	customerID string) error {
	if !subscription.BillingAccountID.Valid {
		return fmt.Errorf("subscription %s isn't linked to any billing account", subscription.ID)
	}
	billingAccount, err := s.billingRepo.FindByID(ctx, tx, subscription.BillingAccountID.String)
	if err != nil {
		return err
	}
	if billingAccount.StripeCustomerID.Valid {
		return nil
	}
	billingAccount.StripeCustomerID = null.StringFrom(customerID)
	return s.billingRepo.UpdateStripeCustomerID(ctx, tx, billingAccount)
}

type Owner struct {
	ID   string
	Type string
}

// EnsureBillingAccountForOwner will create a BillingAccount for this ownerID,
// or return the record if a BillingAccount already exists.
func (s *Service) EnsureBillingAccountForOwner(ctx context.Context, tx database.Tx, owner *Owner) (*model.BillingAccount, apierror.Error) {
	billingAccount, err := s.billingRepo.QueryByOwnerID(ctx, tx, owner.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if billingAccount == nil {
		billingAccount = &model.BillingAccount{
			BillingAccount: &sqbmodel.BillingAccount{
				OwnerID: owner.ID,
			},
		}
		err = s.billingRepo.Insert(ctx, tx, billingAccount)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	if billingAccount.StripeCustomerID.Valid {
		return billingAccount, nil
	}

	var ownerName string
	switch owner.Type {
	case constants.OrganizationResource:
		organization, err := s.organizationRepo.QueryByID(ctx, tx, owner.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		if organization == nil {
			return nil, apierror.ResourceNotFound()
		}
		ownerName = organization.Name
	case constants.UserResource:
		user, err := s.userRepo.QueryByID(ctx, tx, owner.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		if user == nil {
			return nil, apierror.ResourceNotFound()
		}
		ownerName = user.Name()
	default:
		return nil, apierror.Unexpected(fmt.Errorf("unknown owner type %s", owner.Type))
	}

	stripeCustomer, err := s.paymentProvider.CreateCustomer(ownerName, owner.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	billingAccount.StripeCustomerID = null.StringFrom(stripeCustomer.ID)
	if err := s.billingRepo.UpdateStripeCustomerID(ctx, tx, billingAccount); err != nil {
		return nil, apierror.Unexpected(err)
	}
	return billingAccount, nil
}

func (s *Service) ClearPaidSubscriptionColumns(ctx context.Context, tx database.Tx, subscription *model.Subscription) error {
	if err := s.subscriptionMetricsRepo.DeleteBySubscriptionID(ctx, tx, subscription.ID); err != nil {
		return err
	}

	subscription.StripeSubscriptionID = null.StringFromPtr(nil)
	subscription.BillingCycleAnchor = null.TimeFromPtr(nil)
	return s.subscriptionsRepo.Update(ctx, tx, subscription,
		sqbmodel.SubscriptionColumns.StripeSubscriptionID,
		sqbmodel.SubscriptionColumns.BillingCycleAnchor,
	)
}

func (s *Service) BillableResourceForSubscription(
	ctx context.Context,
	tx database.Tx,
	subscription *model.Subscription,
) (BillableResource, error) {
	billableResource := BillableResource{
		ID:                  subscription.ResourceID,
		Type:                subscription.ResourceType,
		UnsupportedFeatures: NoUnsupportedFeatures,
	}

	switch subscription.ResourceType {
	case constants.ApplicationResource:
		app, err := s.applicationRepo.QueryByID(ctx, tx, subscription.ResourceID)
		if err != nil {
			return billableResource, err
		}
		if app == nil {
			return billableResource, nil
		}
		return s.BillableResourceForApplication(ctx, tx, app), nil
	case constants.OrganizationResource:
		organization, err := s.organizationRepo.QueryByID(ctx, tx, subscription.ResourceID)
		if err != nil {
			return billableResource, err
		}
		if organization == nil {
			return billableResource, nil
		}
		return BillableResourceForOrganization(organization), nil
	default:
		return billableResource, fmt.Errorf("unknown subscription resource type %s", subscription.ResourceType)
	}
}

func (s *Service) BillableResourceForApplication(
	ctx context.Context,
	tx database.Tx,
	application *model.Application,
) BillableResource {
	return BillableResource{
		ID:   application.ID,
		Name: application.Name,
		Type: constants.ApplicationResource,
		UnsupportedFeatures: func(plans ...*model.SubscriptionPlan) ([]string, error) {
			return s.UnsupportedFeaturesForApplication(ctx, tx, application.ID, plans...)
		},
	}
}

func BillableResourceForOrganization(organization *model.Organization) BillableResource {
	return BillableResource{
		ID:                  organization.ID,
		Name:                organization.Name,
		Type:                constants.OrganizationResource,
		UnsupportedFeatures: NoUnsupportedFeatures,
	}
}

func (s *Service) UnsupportedFeaturesForApplication(ctx context.Context, tx database.Tx, applicationID string, plans ...*model.SubscriptionPlan) ([]string, error) {
	productionInstance, err := s.instanceRepo.QueryByApplicationAndEnvironmentType(ctx, tx, applicationID, constants.ETProduction)
	if err != nil {
		return nil, err
	} else if productionInstance == nil {
		return []string{}, nil
	}
	env, err := s.environmentService.Load(ctx, tx, productionInstance.ID)
	if err != nil {
		return nil, err
	}
	return s.featureService.UnsupportedFeatures(ctx, tx, env, productionInstance.CreatedAt, plans...)
}

const (
	// v1 is hard-coded, v2 is sent from the browser because it's temporary:
	// it starts with /v2 now but we will drop the /v2 part once it's ready
	v1BillingPath = "/applications/%s/billing"

	checkoutResultParam = "checkout"
	clerkSessionParam   = "clerk_session_id"

	// We need to append this as raw string otherwise url.Values.Add escapes the
	// braces {} and Stripe doesn't like it :facepalm:
	stripeCheckoutParam = "checkout_session_id"
	rawCheckoutParam    = "&" + stripeCheckoutParam + "={CHECKOUT_SESSION_ID}"
)

func returnQueryVals(sessID string) url.Values {
	return url.Values{
		clerkSessionParam: []string{sessID},
	}
}

func successQueryVals(sessID string) url.Values {
	v := returnQueryVals(sessID)
	v.Add(checkoutResultParam, "success")
	return v
}

func cancelQueryVals(sessID string) url.Values {
	v := returnQueryVals(sessID)
	v.Add(checkoutResultParam, "canceled")
	return v
}

func dashboardSuccessURL(appID, sessID, origin string, returnURL *url.URL) string {
	if returnURL.String() == "" {
		return dashboardV1SuccessURL(appID, sessID, origin)
	}
	return dashboardV2SuccessURL(returnURL, sessID, origin)
}

func dashboardV1SuccessURL(appID, sessID, origin string) string {
	u := v1BillingURL(appID, origin)
	putValsToURL(successQueryVals(sessID), u)
	return u.String() + rawCheckoutParam
}

func dashboardV2SuccessURL(returnURL *url.URL, sessID, origin string) string {
	u := v2BillingURL(returnURL, origin)
	putValsToURL(successQueryVals(sessID), u)
	return u.String() + rawCheckoutParam
}

func dashboardCancelURL(appID, sessID, origin string, returnURL *url.URL) string {
	if returnURL.String() == "" {
		return dashboardV1CancelURL(appID, sessID, origin)
	}
	return dashboardV2CancelURL(returnURL, sessID, origin)
}

func dashboardV1CancelURL(appID, sessID, origin string) string {
	u := v1BillingURL(appID, origin)
	putValsToURL(cancelQueryVals(sessID), u)
	return u.String() + rawCheckoutParam
}

func dashboardV2CancelURL(returnURL *url.URL, sessID, origin string) string {
	u := v2BillingURL(returnURL, origin)
	putValsToURL(cancelQueryVals(sessID), u)
	return u.String() + rawCheckoutParam
}

func dashboardReturnURL(appID, sessID, origin string, returnURL *url.URL) string {
	if returnURL.String() == "" {
		return dashboardV1ReturnURL(appID, sessID, origin)
	}
	return dashboardV2ReturnURL(returnURL, sessID, origin)
}

func dashboardV1ReturnURL(appID, sessID, origin string) string {
	u := v1BillingURL(appID, origin)
	putValsToURL(returnQueryVals(sessID), u)
	return u.String()
}

func dashboardV2ReturnURL(returnURL *url.URL, sessID, origin string) string {
	u := v2BillingURL(returnURL, origin)
	putValsToURL(returnQueryVals(sessID), u)
	return u.String()
}

// v1BillingURL returns the url of the billing page of an app in v1 dashboard.
func v1BillingURL(appID, origin string) *url.URL {
	u, _ := url.Parse(origin) // the origin is already validated at the http handler level
	u.Path = fmt.Sprintf(v1BillingPath, appID)
	return u
}

// v2BillingURL returns the url of the billing page of an app in v2 dashboard.
func v2BillingURL(targetURL *url.URL, origin string) *url.URL {
	u, _ := url.Parse(origin) // the origin is already validated at the http handler level
	u.Path = targetURL.Path
	u.RawQuery = targetURL.RawQuery
	return u
}

// putValsToURL sets replaces query params in the url with the ones provided. It
// only supports single values. Removes previously set "checkout_session_id" as
// it needs to be added as raw string.
func putValsToURL(vals url.Values, u *url.URL) {
	uv := u.Query()
	// prevent special parameters leaking between checkout/return URLs otherwise
	// the UI shows invalid notifications
	uv.Del(stripeCheckoutParam)
	uv.Del(checkoutResultParam)
	uv.Del(clerkSessionParam)
	for k := range vals {
		uv.Add(k, vals.Get(k))
	}
	u.RawQuery = uv.Encode()
}
