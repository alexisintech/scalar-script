package billing

import (
	"context"
	"net/url"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/serializer"
	"clerk/api/serializer/billing_change_plan"
	"clerk/api/serializer/billing_current"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/validate"

	"github.com/stripe/stripe-go/v72"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	db                         database.Database
	billingConnector           billing.Connector
	paymentProvider            billing.PaymentProvider
	serializer                 *serializer.Factory
	billingCheckoutSessionRepo *repository.BillingCheckoutSession
	billingPlanRepo            *repository.BillingPlans
	billingSubscriptionRepo    *repository.BillingSubscriptions
}

func NewService(deps clerk.Deps, billingConnector billing.Connector, paymentProvider billing.PaymentProvider) *Service {
	return &Service{
		db:                         deps.DB(),
		billingConnector:           billingConnector,
		paymentProvider:            paymentProvider,
		serializer:                 serializer.NewFactory(),
		billingCheckoutSessionRepo: repository.NewBillingCheckoutSession(),
		billingPlanRepo:            repository.NewBillingPlans(),
		billingSubscriptionRepo:    repository.NewBillingSubscriptions(),
	}
}

func (s *Service) GetAvailablePlansForCustomerType(ctx context.Context, customerType string) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	plans, err := s.billingPlanRepo.FindAllByInstanceAndCustomerType(ctx, s.db, env.Instance.ID, customerType)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	data := make([]any, len(plans))
	for i, plan := range plans {
		data[i] = serialize.BillingPlan(plan)
	}
	return serialize.Paginated(data, int64(len(data))), nil
}

type StartPortalSessionParams struct {
	ResourceID string
	ReturnURL  string
}

func (s *Service) StartPortalSession(ctx context.Context, params StartPortalSessionParams) (*serialize.BillingPortalSessionResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)

	subscription, err := s.billingSubscriptionRepo.QueryByResourceID(ctx, s.db, params.ResourceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if subscription == nil {
		return nil, apierror.ResourceNotFound()
	}

	if !subscription.StripeCustomerID.Valid {
		if err := s.createCustomer(ctx, env.Instance, user, subscription); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	portalSessionParams := &stripe.BillingPortalSessionParams{
		Customer:  subscription.StripeCustomerID.Ptr(),
		ReturnURL: stripe.String(params.ReturnURL),
	}
	portalSessionParams.SetStripeAccount(env.Instance.ExternalBillingAccountID.String)
	redirectURL, err := s.paymentProvider.CustomerPortalURL(cenv.Get(cenv.BillingStripeSecretKey), portalSessionParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return serialize.NewBillingPortalSession(redirectURL), nil
}

func (s *Service) GetCurrentForUser(ctx context.Context) (*billing_current.SerializedBillingCurrent, apierror.Error) {
	user := requesting_user.FromContext(ctx)

	subscription, err := s.billingSubscriptionRepo.QueryByResourceID(ctx, s.db, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if subscription == nil {
		return nil, apierror.ResourceNotFound()
	}

	return s.getCurrentForSubscription(ctx, subscription)
}

func (s *Service) GetCurrentForOrganization(ctx context.Context, orgID string) (*billing_current.SerializedBillingCurrent, apierror.Error) {
	subscription, err := s.billingSubscriptionRepo.QueryByResourceID(ctx, s.db, orgID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if subscription == nil {
		return nil, apierror.ResourceNotFound()
	}

	return s.getCurrentForSubscription(ctx, subscription)
}

func (s *Service) getCurrentForSubscription(ctx context.Context, subscription *model.BillingSubscription) (*billing_current.SerializedBillingCurrent, apierror.Error) {
	env := environment.FromContext(ctx)

	plan, err := s.billingPlanRepo.FindByID(ctx, s.db, subscription.BillingPlanID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	stripeSecretKey := cenv.Get(cenv.BillingStripeSecretKey)

	var nextInvoice *stripe.Invoice
	if subscription.StripeSubscriptionID.Valid && plan.PriceInCents > 0 {
		nextInvoice, err = s.paymentProvider.FetchNextInvoiceForExternalBillingAccount(
			stripeSecretKey,
			env.Instance.ExternalBillingAccountID.String,
			subscription.StripeCustomerID.String,
			subscription.StripeSubscriptionID.String,
		)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	billingConnector := s.billingConnector.Connect(env.Instance.ExternalBillingAccountID.String)

	var paymentMethod *billing.PaymentMethod
	if subscription.StripeCustomerID.Valid {
		paymentMethod, err = s.resolveDefaultPaymentMethodForCustomer(ctx, billingConnector, subscription, paymentMethod)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	return s.serializer.BillingCurrent().Serialize(ctx, subscription, plan, nextInvoice, paymentMethod), nil
}

func (s *Service) resolveDefaultPaymentMethodForCustomer(ctx context.Context, billingConnector billing.Provider, subscription *model.BillingSubscription, paymentMethod *billing.PaymentMethod) (*billing.PaymentMethod, error) {
	customer, err := billingConnector.Customer(ctx, subscription.StripeCustomerID.String)
	if err != nil {
		return nil, err
	}

	paymentMethods, err := billingConnector.PaymentMethods(ctx, customer.ID, billing.PaymentMethodsParams{
		Type: stripe.String(string(stripe.PaymentMethodTypeCard)),
	})
	if err != nil {
		return nil, err
	}

	// find the default payment method, if any
	if customer.DefaultPaymentMethodID != nil {
		for _, pm := range paymentMethods {
			if pm.ID == *customer.DefaultPaymentMethodID {
				return pm, nil
			}
		}
	}

	// if no default payment method is found, use the first one
	if paymentMethod == nil && len(paymentMethods) > 0 {
		return paymentMethods[0], nil
	}

	// if no payment methods are found, return nil
	return nil, nil
}

type ChangePlanForUserParams struct {
	changePlanParams
}

func (p ChangePlanForUserParams) validate() apierror.Error {
	return p.changePlanParams.validate()
}

func (s *Service) ChangePlanForUser(ctx context.Context, params ChangePlanForUserParams) (*billing_change_plan.SerializedBillingChangePlan, apierror.Error) {
	user := requesting_user.FromContext(ctx)

	if err := params.validate(); err != nil {
		return nil, err
	}

	return s.changePlan(ctx, user.ID, changePlanParams{
		PlanKey:          params.PlanKey,
		SuccessReturnURL: params.SuccessReturnURL,
		CancelReturnURL:  params.CancelReturnURL,
	})
}

type ChangePlanForOrganizationParams struct {
	changePlanParams
	OrganizationID string
}

func (p ChangePlanForOrganizationParams) validate() apierror.Error {
	var formErrs apierror.Error
	if p.OrganizationID == "" {
		formErrs = apierror.Combine(formErrs, apierror.FormMissingParameter("organization_id"))
	}

	return apierror.Combine(formErrs, p.changePlanParams.validate())
}

func (s *Service) ChangePlanForOrganization(ctx context.Context, params ChangePlanForOrganizationParams) (*billing_change_plan.SerializedBillingChangePlan, apierror.Error) {
	if err := params.validate(); err != nil {
		return nil, err
	}

	return s.changePlan(ctx, params.OrganizationID, changePlanParams{
		PlanKey:          params.PlanKey,
		SuccessReturnURL: params.SuccessReturnURL,
		CancelReturnURL:  params.CancelReturnURL,
	})
}

type changePlanParams struct {
	PlanKey          string
	SuccessReturnURL string
	CancelReturnURL  string
}

func (p changePlanParams) validate() apierror.Error {
	var formErrs apierror.Error
	if p.PlanKey == "" {
		formErrs = apierror.Combine(formErrs, apierror.FormMissingParameter("plan_key"))
	}

	if p.SuccessReturnURL == "" {
		formErrs = apierror.Combine(formErrs, apierror.FormMissingParameter("success_return_url"))
	}

	if ok := validate.URL(p.SuccessReturnURL); !ok {
		formErrs = apierror.Combine(formErrs, apierror.FormInvalidParameterFormat("success_return_url", "Must be a valid URL."))
	}

	if p.CancelReturnURL != "" {
		if ok := validate.URL(p.CancelReturnURL); !ok {
			formErrs = apierror.Combine(formErrs, apierror.FormInvalidParameterFormat("cancel_return_url", "Must be a valid URL."))
		}
	}

	return formErrs
}

func (s *Service) changePlan(ctx context.Context, resourceID string, params changePlanParams) (*billing_change_plan.SerializedBillingChangePlan, apierror.Error) {
	env := environment.FromContext(ctx)

	plan, err := s.billingPlanRepo.QueryByInstanceAndPlanKey(ctx, s.db, env.Instance.ID, params.PlanKey)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if plan == nil {
		return nil, apierror.ResourceNotFound()
	}

	subscription, err := s.billingSubscriptionRepo.QueryByResourceID(ctx, s.db, resourceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if subscription == nil {
		return nil, apierror.ResourceNotFound()
	}
	if subscription.BillingPlanID == plan.ID {
		return nil, apierror.BillingPlanAlreadyActive()
	}

	cancelURL := params.CancelReturnURL
	if cancelURL == "" {
		cancelURL = params.SuccessReturnURL
	}

	fapiHost := env.Domain.AuthHost()
	redirectURL, checkoutSessionID, err := s.billingConnector.
		Connect(env.Instance.ExternalBillingAccountID.String).
		CheckoutURL(ctx, billing.CheckoutURLParams{
			SuccessURL: formatCallbackURL(fapiHost, params.SuccessReturnURL, constants.PaymentStatusComplete),
			CancelURL:  formatCallbackURL(fapiHost, cancelURL, constants.PaymentStatusCanceled),
			PriceID:    plan.StripePriceID.String,
			ResourceID: subscription.ResourceID,
			CustomerID: subscription.StripeCustomerID.Ptr(),
		})
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	checkoutSession := &model.BillingCheckoutSession{BillingCheckoutSession: &sqbmodel.BillingCheckoutSession{
		SubscriptionID:          subscription.ID,
		StripeCheckoutSessionID: checkoutSessionID,
	}}
	if err := s.billingCheckoutSessionRepo.Insert(ctx, s.db, checkoutSession); err != nil {
		return nil, nil
	}

	return s.serializer.BillingChangePlan().Serialize(ctx, redirectURL), nil
}

func formatCallbackURL(fapiHost string, returnURL, status string) string {
	u := url.URL{Scheme: "https", Host: fapiHost}
	u.Path = "/v1/billing_change_plan_callback"

	queryParams := u.Query()
	queryParams.Set("redirect_url", returnURL)
	queryParams.Set("status", status)
	u.RawQuery = queryParams.Encode()

	// We cannot use the query params for `{CHECKOUT_SESSION_ID}` because it gets
	// escaped and Stripe doesn't like that.
	return u.String() + "&checkout_session_id={CHECKOUT_SESSION_ID}"
}

type ChangePlanCallbackParams struct {
	RedirectURL       string
	CheckoutSessionID string
	Status            string
}

func (p *ChangePlanCallbackParams) validate() apierror.Error {
	var formErrs apierror.Error
	if p.CheckoutSessionID == "" {
		formErrs = apierror.Combine(formErrs, apierror.FormMissingParameter("checkout_session_id"))
	}

	if p.Status == "" {
		formErrs = apierror.Combine(formErrs, apierror.FormMissingParameter("status"))
	}

	acceptedStatuses := set.New[string](constants.PaymentStatusComplete, constants.PaymentStatusCanceled)
	if !acceptedStatuses.Contains(p.Status) {
		formErrs = apierror.Combine(formErrs, apierror.FormInvalidParameterFormat("status", "Must be one of: "+strings.Join(acceptedStatuses.Array(), ", ")))
	}

	if p.RedirectURL == "" {
		formErrs = apierror.Combine(formErrs, apierror.FormMissingParameter("redirect_url"))
	}

	return formErrs
}

func (s *Service) ChangePlanCallback(ctx context.Context, params ChangePlanCallbackParams) apierror.Error {
	if err := params.validate(); err != nil {
		return err
	}

	// TODO(kostas): Validate that the URL is an allowed redirect URL
	// https://github.com/clerk/clerk_go/pull/6027/files#r1546798789

	checkoutSession, err := s.billingCheckoutSessionRepo.QueryByStripeCheckoutSessionID(ctx, s.db, params.CheckoutSessionID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if checkoutSession == nil {
		return apierror.BillingCheckoutSessionNotFound(params.CheckoutSessionID)
	}

	if checkoutSession.Status != constants.PaymentStatusInitiated {
		return apierror.BillingCheckoutSessionAlreadyProcessed(params.CheckoutSessionID)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		checkoutSession.Status = params.Status
		if err := s.billingCheckoutSessionRepo.UpdateStatus(ctx, tx, checkoutSession); err != nil {
			return true, err
		}

		// if checkout wasn't completed, we don't update the subscription
		if params.Status != constants.PaymentStatusComplete {
			return false, nil
		}

		if err := s.handleSuccessfulChangePlan(ctx, tx, checkoutSession); err != nil {
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

func (s *Service) handleSuccessfulChangePlan(ctx context.Context, tx database.Tx, checkoutSession *model.BillingCheckoutSession) error {
	env := environment.FromContext(ctx)

	billingConnector := s.billingConnector.Connect(env.Instance.ExternalBillingAccountID.String)

	checkoutSessionData, err := billingConnector.CheckoutSession(ctx, checkoutSession.StripeCheckoutSessionID)
	if err != nil {
		return err
	}

	subscription, err := s.billingSubscriptionRepo.FindByID(ctx, tx, checkoutSession.SubscriptionID)
	if err != nil {
		return err
	}

	// cancel the current subscription
	if subscription.StripeSubscriptionID.Valid {
		if err := billingConnector.CancelSubscription(ctx, subscription.StripeSubscriptionID.String); err != nil {
			return err
		}
	}

	// update the default payment method for the customer
	if err := billingConnector.UpdateCustomer(
		ctx,
		checkoutSessionData.Subscription.Customer.ID,
		billing.UpdateCustomerParams{
			DefaultPaymentMethodID: &checkoutSessionData.Subscription.PaymentMethodID,
		},
	); err != nil {
		return err
	}

	// find the plan based on the product ID
	// Note: We assume that the subscription has only a price and a product. This is based on the current implementation.
	plan, err := s.billingPlanRepo.FindByStripeProductID(ctx, tx, checkoutSessionData.Subscription.Prices[0].ProductID)
	if err != nil {
		return err
	}
	subscription.BillingPlanID = plan.ID

	// update the subscription with the new checkout session data
	subscription.StripeSubscriptionID = null.StringFrom(checkoutSessionData.Subscription.ID)
	subscription.StripeCustomerID = null.StringFrom(checkoutSessionData.Subscription.Customer.ID)

	updateCols := []string{
		sqbmodel.BillingSubscriptionColumns.BillingPlanID,
		sqbmodel.BillingSubscriptionColumns.StripeSubscriptionID,
		sqbmodel.BillingSubscriptionColumns.StripeCustomerID,
	}
	return s.billingSubscriptionRepo.Update(ctx, tx, subscription, updateCols...)
}

func (s *Service) createCustomer(ctx context.Context, instance *model.Instance, user *model.User, subscription *model.BillingSubscription) error {
	params := billing.CreateCustomerParams{}

	name := user.Name()
	if name != "" {
		params.Name = &name
	}

	customer, err := s.billingConnector.
		Connect(instance.ExternalBillingAccountID.String).
		CreateCustomer(ctx, params)
	if err != nil {
		return err
	}

	subscription.StripeCustomerID = null.StringFrom(customer.ID)
	return s.billingSubscriptionRepo.UpdateStripeCustomerID(ctx, s.db, subscription)
}
