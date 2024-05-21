package pricing

import (
	"context"
	"fmt"
	"net/url"

	"clerk/api/apierror"
	dapiserialize "clerk/api/dapi/serialize"
	"clerk/api/serialize"
	"clerk/api/shared/environment"
	"clerk/api/shared/features"
	"clerk/api/shared/pricing"
	"clerk/api/shared/usage"
	"clerk/model"
	"clerk/model/sqbmodel"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/jobs"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"

	"github.com/go-playground/validator/v10"
	"github.com/stripe/stripe-go/v72"
	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	db              database.Database
	paymentProvider clerkbilling.PaymentProvider
	gueClient       *gue.Client

	// services
	billingService     *pricing.Service
	environmentService *environment.Service
	featureService     *features.Service
	usageService       *usage.Service

	// repositories
	appRepo                 *repository.Applications
	instanceRepo            *repository.Instances
	organizationRepo        *repository.Organization
	plansRepo               *repository.SubscriptionPlans
	subscriptionMetricsRepo *repository.SubscriptionMetrics
	subscriptionPricesRepo  *repository.SubscriptionPrices
	subscriptionRepo        *repository.Subscriptions
	subscriptionPlanRepo    *repository.SubscriptionPlans
	subscriptionProductRepo *repository.SubscriptionProduct
}

func NewService(deps clerk.Deps, paymentProvider clerkbilling.PaymentProvider) *Service {
	return &Service{
		db:                      deps.DB(),
		paymentProvider:         clerkbilling.NewCachedPaymentProvider(deps.Clock(), deps.DB(), paymentProvider),
		gueClient:               deps.GueClient(),
		billingService:          pricing.NewService(deps.DB(), deps.GueClient(), deps.Clock(), paymentProvider),
		environmentService:      environment.NewService(),
		featureService:          features.NewService(deps.DB(), deps.GueClient()),
		usageService:            usage.NewService(deps.Clock(), deps.DB(), deps.GueClient(), paymentProvider),
		appRepo:                 repository.NewApplications(),
		instanceRepo:            repository.NewInstances(),
		organizationRepo:        repository.NewOrganization(),
		plansRepo:               repository.NewSubscriptionPlans(),
		subscriptionMetricsRepo: repository.NewSubscriptionMetrics(),
		subscriptionPricesRepo:  repository.NewSubscriptionPrices(),
		subscriptionRepo:        repository.NewSubscriptions(),
		subscriptionPlanRepo:    repository.NewSubscriptionPlans(),
		subscriptionProductRepo: repository.NewSubscriptionProduct(),
	}
}

type RedirectResponse struct {
	URL string `json:"url"`
}

type CheckoutSessionRedirectParams struct {
	Plans         []string `json:"plans" validate:"required"`
	ReturnURL     string   `json:"return_url" validate:"required"`
	ApplicationID string
	SessionID     string
	Origin        string
	OwnerID       string
	OwnerType     string
}

func (params CheckoutSessionRedirectParams) Validate() apierror.Error {
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

// CreateAppCheckoutSession prepares a Stripe Checkout Session and returns the URL
// for it.
func (s *Service) CreateAppCheckoutSession(ctx context.Context, params CheckoutSessionRedirectParams) (*RedirectResponse, apierror.Error) {
	apiErr := params.Validate()

	returnURL, err := url.Parse(params.ReturnURL)
	if err != nil {
		apiErr = apierror.Combine(apiErr, apierror.FormInvalidParameterFormat("return_url"))
	}

	if apiErr != nil {
		return nil, apiErr
	}

	var redirectURL string
	// Start transaction to be able to SELECT app for UPDATE
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		app, err := s.appRepo.QueryByID(ctx, tx, params.ApplicationID)
		if err != nil {
			return true, err
		}
		if app == nil {
			return true, apierror.ApplicationNotFound(params.ApplicationID)
		}

		redirectURL, err = s.billingService.CreateCheckoutSession(ctx, tx, pricing.CreateCheckoutSessionParams{
			Owner: &pricing.Owner{
				ID:   params.OwnerID,
				Type: params.OwnerType,
			},
			NewPlanIDs: params.Plans,
			Resource:   s.billingService.BillableResourceForApplication(ctx, tx, app),
			ReturnURL:  returnURL,
			SessionID:  params.SessionID,
			Origin:     params.Origin,
		})
		if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		// Check if already an api error
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}

		return nil, apierror.Unexpected(txErr)
	}

	return &RedirectResponse{
		URL: redirectURL,
	}, nil
}

type OrganizationCheckoutSessionParams struct {
	OrganizationID string
	PlanID         string
	SessionID      string
	ReturnURL      *url.URL
	Origin         string
}

// CreateOrganizationCheckoutSession prepares a Stripe Checkout Session for the given organization
// and plan, and returns the URL for it.
func (s *Service) CreateOrganizationCheckoutSession(ctx context.Context, params OrganizationCheckoutSessionParams) (*RedirectResponse, apierror.Error) {
	var redirectURL string
	// Start transaction to be able to SELECT app for UPDATE
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		organization, err := s.organizationRepo.QueryByID(ctx, tx, params.OrganizationID)
		if err != nil {
			return true, err
		}
		if organization == nil {
			return true, apierror.OrganizationNotFound()
		}

		redirectURL, err = s.billingService.CreateCheckoutSession(ctx, tx, pricing.CreateCheckoutSessionParams{
			Owner: &pricing.Owner{
				ID:   params.OrganizationID,
				Type: constants.OrganizationResource,
			},
			NewPlanIDs: []string{params.PlanID},
			Resource:   pricing.BillableResourceForOrganization(organization),
			ReturnURL:  params.ReturnURL,
			SessionID:  params.SessionID,
			Origin:     params.Origin,
		})
		if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		// Check if already an api error
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}

		return nil, apierror.Unexpected(txErr)
	}

	return &RedirectResponse{
		URL: redirectURL,
	}, nil
}

// CreateApplicationCustomerPortalSession prepares a Stripe Customer Portal Session and returns the URL for it.
func (s *Service) CreateApplicationCustomerPortalSession(ctx context.Context, applicationID, sessionID, origin string, returnURL *url.URL) (*RedirectResponse, apierror.Error) {
	app, err := s.appRepo.QueryByID(ctx, s.db, applicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if app == nil {
		return nil, apierror.ApplicationNotFound(applicationID)
	}

	ownerID, ownerType := sdkutils.OwnerFrom(ctx)
	var redirectURL string
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		redirectURL, err = s.billingService.CreateCustomerPortalSession(ctx, tx, pricing.CreateCustomerPortalSessionParams{
			Owner: &pricing.Owner{
				ID:   ownerID,
				Type: ownerType,
			},
			ResourceID: app.ID,
			ReturnURL:  returnURL,
			SessionID:  sessionID,
			Origin:     origin,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return &RedirectResponse{
		URL: redirectURL,
	}, nil
}

// CreateOrganizationCustomerPortalSession prepares a Stripe Customer Portal Session and returns the URL for it.
func (s *Service) CreateOrganizationCustomerPortalSession(ctx context.Context, organizationID, sessionID, origin string, returnURL *url.URL) (*RedirectResponse, apierror.Error) {
	var redirectURL string
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		redirectURL, err = s.billingService.CreateCustomerPortalSession(ctx, tx, pricing.CreateCustomerPortalSessionParams{
			Owner: &pricing.Owner{
				ID:   organizationID,
				Type: constants.OrganizationResource,
			},
			ResourceID: organizationID,
			ReturnURL:  returnURL,
			SessionID:  sessionID,
			Origin:     origin,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return &RedirectResponse{
		URL: redirectURL,
	}, nil
}

// CheckoutSessionCompleted handles the webhook after the Stripe Session is
// completed.
func (s *Service) CheckoutSessionCompleted(ctx context.Context, checkoutSession stripe.CheckoutSession) apierror.Error {
	// Start transaction to be able to SELECT app for UPDATE
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		subscription, err := s.subscriptionRepo.FindByResourceIDForUpdate(ctx, tx, checkoutSession.ClientReferenceID)
		if err != nil {
			return true, err
		}

		billableResource, err := s.billingService.BillableResourceForSubscription(ctx, tx, subscription)
		if err != nil {
			return true, err
		}
		err = s.billingService.CheckoutSessionCompleted(ctx, tx, pricing.CheckoutSessionCompletedParams{
			CheckoutSession: checkoutSession,
			Resource:        billableResource,
			Subscription:    subscription,
		})
		if err != nil {
			return true, err
		}

		switch billableResource.Type {
		case constants.ApplicationResource:
			err := jobs.ReportUsage(ctx, s.gueClient, jobs.ReportUsageArgs{
				ResourceID:   subscription.ResourceID,
				ResourceType: subscription.ResourceType,
				Force:        true,
			})
			if err != nil {
				return true, err
			}
		case constants.OrganizationResource:
			subscriptionPlans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, subscription.ID)
			if err != nil {
				return true, err
			}
			err = s.organizationRepo.UpdateMaxAllowedMemberships(ctx, tx, &model.Organization{
				Organization: &sqbmodel.Organization{
					ID:                    billableResource.ID,
					MaxAllowedMemberships: model.MaxAllowedOrganizationMemberships(subscriptionPlans),
				},
			})
			if err != nil {
				return true, err
			}
			err = jobs.ReportUsage(ctx, s.gueClient, jobs.ReportUsageArgs{
				ResourceID:   subscription.ResourceID,
				ResourceType: subscription.ResourceType,
				Force:        true,
			}, jobs.WithTx(tx))
			if err != nil {
				return true, err
			}
		default:
			return true, fmt.Errorf("no post-switch billing action for billable resource of type %s", billableResource.Type)
		}

		return false, nil
	})
	if txErr != nil {
		// Check if already an api error
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}

		return apierror.Unexpected(txErr)
	}

	return nil
}

type CheckoutValidateParams struct {
	ApplicationID string   `json:"-"`
	Plans         []string `json:"plans"`
	WithRefund    bool     `json:"with_refund"`
}

// CheckoutValidate checks that the settings for all instances of an application
// are supported by a plan.
func (s *Service) CheckoutValidate(ctx context.Context, params CheckoutValidateParams) (*dapiserialize.ApplicationSubscriptionValidationResponse, apierror.Error) {
	newPlans, err := s.plansRepo.FindAllAvailableByIDsAndScope(ctx, s.db, params.ApplicationID, params.Plans, constants.ApplicationResource)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if len(newPlans) == 0 {
		return nil, apierror.ResourceNotFound()
	}

	subscription, err := s.subscriptionRepo.FindByResourceID(ctx, s.db, params.ApplicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var refundAmount int64
	if params.WithRefund && subscription.StripeSubscriptionID.Valid {
		stripeSubscription, err := s.paymentProvider.FetchSubscription(subscription.StripeSubscriptionID.String)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		remainingStripeProducts := set.New[string]()
		for _, plan := range newPlans {
			remainingStripeProducts.Insert(plan.StripeProductID.String)
		}

		toRemove := make([]*stripe.SubscriptionItem, 0)
		for _, subscriptionItem := range stripeSubscription.Items.Data {
			if subscriptionItem.Price.Product != nil && !remainingStripeProducts.Contains(subscriptionItem.Price.Product.ID) {
				toRemove = append(toRemove, subscriptionItem)
			}
		}

		var refundItems map[string]int64
		if len(toRemove) > 0 {
			refundItems, err = s.paymentProvider.DetermineRefundItems(stripeSubscription.ID, toRemove)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}
		}

		for _, amount := range refundItems {
			refundAmount += amount
		}
	}

	productionInstance, err := s.instanceRepo.QueryByApplicationAndEnvironmentType(ctx, s.db, params.ApplicationID, constants.ETProduction)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if productionInstance == nil {
		return dapiserialize.ApplicationSubscriptionValidation([]string{}, refundAmount), nil
	}

	env, err := s.environmentService.Load(ctx, s.db, productionInstance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	unsupportedFeatures, err := s.featureService.UnsupportedFeatures(ctx, s.db, env, productionInstance.CreatedAt, newPlans...)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if !params.WithRefund {
		// old format, return an error with the unsupported features
		if len(unsupportedFeatures) > 0 {
			return nil, apierror.InvalidSubscriptionPlanSwitch(unsupportedFeatures)
		}
		return nil, nil
	}

	return dapiserialize.ApplicationSubscriptionValidation(unsupportedFeatures, refundAmount), nil
}

type CheckoutSuggestAddonsParams struct {
	ApplicationID string   `json:"-"`
	Plans         []string `json:"plans"`
}

func (s *Service) CheckoutSuggestAddons(ctx context.Context, params CheckoutSuggestAddonsParams) ([]*serialize.SubscriptionPlanWithPricesResponse, apierror.Error) {
	newPlans, err := s.plansRepo.FindAllAvailableByIDsAndScope(ctx, s.db, params.ApplicationID, params.Plans, constants.ApplicationResource)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if len(newPlans) == 0 {
		return nil, apierror.ResourceNotFound()
	}

	productionInstance, err := s.instanceRepo.QueryByApplicationAndEnvironmentType(ctx, s.db, params.ApplicationID, constants.ETProduction)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if productionInstance == nil {
		return nil, nil
	}

	env, err := s.environmentService.Load(ctx, s.db, productionInstance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	unsupportedFeatures, err := s.featureService.UnsupportedFeatures(ctx, s.db, env, productionInstance.CreatedAt, newPlans...)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if len(unsupportedFeatures) == 0 {
		return nil, nil
	}

	allAddons := set.New[string]()
	for _, plan := range newPlans {
		allAddons.Insert(plan.Addons...)
	}

	addons, err := s.plansRepo.FindAllByIDs(ctx, s.db, allAddons.Array())
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var plans []*serialize.SubscriptionPlanWithPricesResponse
	for _, addon := range addons {
		if set.New[string](addon.Features...).IsOverlapping(set.New[string](unsupportedFeatures...)) {
			prices, err := s.subscriptionPricesRepo.FindAllByStripeProduct(ctx, s.db, addon.StripeProductID.String)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}
			plans = append(plans, serialize.SubscriptionPlanWithPrices(model.NewSubscriptionPlanWithPrices(addon, prices)))
		}
	}

	return plans, nil
}

// CustomerSubscriptionUpdated handles the webhook event after a subscription was manually
// updated via Stripe.
func (s *Service) CustomerSubscriptionUpdated(ctx context.Context, sub stripe.Subscription, previousItems []*stripe.SubscriptionItem) apierror.Error {
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if sub.Items == nil {
			return false, nil
		}
		subscription, err := s.subscriptionRepo.QueryByStripeSubscriptionIDForUpdate(ctx, tx, sub.ID)
		if err != nil {
			return true, err
		} else if subscription == nil {
			return false, nil
		}

		if err := s.syncSubscriptionMetrics(ctx, tx, subscription.ID, sub.Items.Data); err != nil {
			return true, err
		}

		if err := s.syncSubscriptionProducts(ctx, tx, subscription, previousItems, sub.Items.Data); err != nil {
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

	if err := s.purgeCachedResponsesBySubscription(ctx, sub.ID); err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

// CustomerSubscriptionDeleted handles the webhook after a subscription is canceled.
func (s *Service) CustomerSubscriptionDeleted(ctx context.Context, sub stripe.Subscription) apierror.Error {
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err := s.handleCancelSubscription(ctx, tx, sub); err != nil {
			return true, err
		}

		if err := s.handleCancelSubscriptionItems(ctx, tx, sub); err != nil {
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

	if err := s.purgeCachedResponsesBySubscription(ctx, sub.ID); err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

func (s *Service) CustomerDiscountUpdated(ctx context.Context, discount *stripe.Discount) apierror.Error {
	if err := s.purgeCachedResponsesByCustomer(ctx, discount.Customer); err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}

func (s *Service) CustomerUpdated(ctx context.Context, customer *stripe.Customer) apierror.Error {
	if err := s.purgeCachedResponsesByCustomer(ctx, customer.ID); err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}

func (s *Service) InvoiceItemUpdated(ctx context.Context, invoiceItem *stripe.InvoiceItem) apierror.Error {
	if invoiceItem.Subscription == nil {
		return nil
	}
	if err := s.purgeCachedResponsesBySubscription(ctx, invoiceItem.Subscription.ID); err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}

func (s *Service) handleCancelSubscription(ctx context.Context, tx database.Tx, sub stripe.Subscription) error {
	subscription, err := s.subscriptionRepo.QueryByStripeSubscriptionIDForUpdate(ctx, tx, sub.ID)
	if err != nil {
		return err
	}
	if subscription == nil {
		return nil
	}
	if !subscription.StripeSubscriptionID.Valid || subscription.StripeSubscriptionID.String != sub.ID {
		// This case prevents the known race condition we have.
		// In particular, when we switch to a new subscription plan,
		// we cancel the old one, and then we update the subscription
		// details with the new one.
		// However, cancelling the old one will also trigger a Stripe
		// webhook, so this code will be executed.
		// Given that we lock the subscription record, we might be able
		// to retrieve the record only after the previous process has
		// finished. Which means, that by the time we retrieve it,
		// the subscription might not have the correct Stripe subscription
		// id.
		// This is fine, because it was handled by the other flow, so
		// we just return (nothing else to do).
		return nil
	}

	billableResource, err := s.billingService.BillableResourceForSubscription(ctx, tx, subscription)
	if err != nil {
		return err
	}

	freePlan, err := s.subscriptionPlanRepo.FindFirstAvailableAndFreeByResourceType(ctx, tx, subscription.ResourceType)
	if err != nil {
		return err
	}

	err = s.billingService.ClearPaidSubscriptionColumns(ctx, tx, subscription)
	if err != nil {
		return err
	}

	err = s.billingService.SwitchResourceToPlan(ctx, tx, billableResource, subscription, freePlan)
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) handleCancelSubscriptionItems(ctx context.Context, tx database.Tx, sub stripe.Subscription) error {
	stripeSubscriptionItemIDs := make([]string, len(sub.Items.Data))
	for i, item := range sub.Items.Data {
		stripeSubscriptionItemIDs[i] = item.ID
	}
	return s.subscriptionMetricsRepo.DeleteAllByStripeSubscriptionItemIDs(ctx, tx, stripeSubscriptionItemIDs)
}

// Sync the Clerk subscription products with the provided Stripe subscription
// items.
// Stripe subscription items have prices, and each price belongs to a Stripe
// product. As soon as the sync is completed, our subscription will be associated
// with the Clerk products and prices that map to the prices' Stripe products.
func (s *Service) syncSubscriptionProducts(
	ctx context.Context,
	tx database.Tx,
	clerkSubscription *model.Subscription,
	previousItems []*stripe.SubscriptionItem,
	newItems []*stripe.SubscriptionItem,
) error {
	collectProductIDs := func(items []*stripe.SubscriptionItem) set.Set[string] {
		ids := set.New[string]()
		for _, item := range items {
			if item.Price == nil || item.Price.Product == nil {
				continue
			}
			ids.Insert(item.Price.Product.ID)
		}
		return ids
	}
	// Delete all subscription products from the start. It's easier to start
	// from scratch.
	_, err := s.subscriptionProductRepo.DeleteBySubscriptionID(ctx, tx, clerkSubscription.ID)
	if err != nil {
		return err
	}

	// Find all subscription plans that map to the new Stripe products and create
	// a subscription product record for each.
	newProductIDs := collectProductIDs(newItems)
	newPlans, err := s.subscriptionPlanRepo.FindAllByStripeProductID(ctx, tx, newProductIDs.Array()...)
	if err != nil {
		return err
	}
	if len(newPlans) == 0 {
		// Subscription will end up with no plan.
		sentry.CaptureException(ctx, fmt.Errorf("subscription %s ended up without any product", clerkSubscription.ID))
	}
	if len(newPlans) != newProductIDs.Count() {
		// Some of the Stripe plans are missing. We either haven't synced,
		// or the Stripe products are configured incorrectly.
		sentry.CaptureException(ctx, fmt.Errorf("products mismatch: subscription %s, Stripe products %s", clerkSubscription.ID, newProductIDs.Array()))
	}
	for _, plan := range newPlans {
		if err := s.subscriptionProductRepo.Insert(ctx, tx, model.NewSubscriptionProduct(clerkSubscription.ID, plan.ID)); err != nil {
			return err
		}
	}

	// Find the obsolete products and check whether we need to keep any of them
	// in grace period.
	removedProductIDs := collectProductIDs(previousItems)
	removedProductIDs.Subtract(newProductIDs)
	previousPlans, err := s.subscriptionPlanRepo.FindAllByStripeProductID(ctx, tx, removedProductIDs.Array()...)
	if err != nil {
		return err
	}

	billableResource, err := s.billingService.BillableResourceForSubscription(ctx, tx, clerkSubscription)
	if err != nil {
		return err
	}
	err = s.billingService.HandleGracePeriod(ctx, tx, billableResource, clerkSubscription, previousPlans, newPlans...)
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) syncSubscriptionMetrics(ctx context.Context, tx database.Tx, clerkSubscriptionID string, stripeSubscriptionItems []*stripe.SubscriptionItem) error {
	// clear all the existing subscription items to populate only the new ones
	if err := s.subscriptionMetricsRepo.DeleteBySubscriptionID(ctx, tx, clerkSubscriptionID); err != nil {
		return err
	}

	for _, item := range stripeSubscriptionItems {
		if item.Price.Metadata["metric"] == "" || item.Price.Metadata["metric"] == clerkbilling.PriceTypes.Fixed {
			continue
		}
		subscriptionMetric := model.NewSubscriptionMetric(clerkSubscriptionID, item.Price.Metadata["metric"], item.ID)
		if err := s.subscriptionMetricsRepo.Insert(ctx, tx, subscriptionMetric); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) purgeCachedResponsesBySubscription(ctx context.Context, subscriptionID string) error {
	return s.paymentProvider.(*clerkbilling.CachedPaymentProvider).ResetCacheForStripeSubscriptionID(ctx, subscriptionID)
}

func (s *Service) purgeCachedResponsesByCustomer(ctx context.Context, customerID string) error {
	subscriptions, err := s.paymentProvider.FetchCustomerSubscriptions(customerID)
	if err != nil {
		return err
	}

	subscriptionIDs := make([]string, len(subscriptions))
	for i, subscription := range subscriptions {
		subscriptionIDs[i] = subscription.ID
	}

	if err := s.paymentProvider.(*clerkbilling.CachedPaymentProvider).ResetCacheForStripeSubscriptionIDs(ctx, subscriptionIDs); err != nil {
		return err
	}

	return nil
}

// RefreshGracePeriodFeaturesAfterUpdate updates the grace period features (if any) with the
// new unsupported features of the product instance of an application.
// Note that this is called in a middleware, after every mutation method in DAPI.
// If we don't manage to update the grace period features, it's not the end of the world...in
// other words, errors are ignored in this method.
func (s *Service) RefreshGracePeriodFeaturesAfterUpdate(ctx context.Context, instanceID string) {
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		env, err := s.environmentService.Load(ctx, tx, instanceID)
		if err != nil {
			return true, err
		}
		if !env.Instance.IsProduction() || len(env.Subscription.GracePeriodFeatures) == 0 {
			return false, nil
		}

		env.Subscription.GracePeriodFeatures = []string{}
		if err := s.subscriptionRepo.UpdateGracePeriodFeatures(ctx, tx, env.Subscription); err != nil {
			return true, err
		}

		plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, env.Subscription.ID)
		if err != nil {
			return true, err
		}

		env.Subscription.GracePeriodFeatures, err = s.featureService.UnsupportedFeatures(ctx, tx, env, env.Instance.CreatedAt, plans...)
		if err != nil {
			return true, err
		}

		err = s.subscriptionRepo.UpdateGracePeriodFeatures(ctx, tx, env.Subscription)
		return err != nil, err
	})
	if txErr != nil {
		log.Warning(ctx, txErr.Error())
	}
}
