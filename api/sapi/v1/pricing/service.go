package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"clerk/api/apierror"
	"clerk/api/sapi/serialize"
	"clerk/api/sapi/v1/serializable"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/stripe/stripe-go/v72"
	"github.com/volatiletech/null/v8"
)

const (
	EnterprisePlanIDPrefix = "enterprise_"
)

type Service struct {
	clock                   clockwork.Clock
	db                      database.Database
	paymentProvider         billing.PaymentProvider
	validator               *validator.Validate
	subscriptionPlanService *serializable.SubscriptionPlanService
	applicationRepo         *repository.Applications
	subscriptionRepo        *repository.Subscriptions
	subscriptionPlanRepo    *repository.SubscriptionPlans
}

func NewService(clock clockwork.Clock, db database.Database, paymentProvider billing.PaymentProvider) *Service {
	return &Service{
		clock:                   clock,
		db:                      db,
		paymentProvider:         paymentProvider,
		validator:               validator.New(),
		subscriptionPlanService: serializable.NewSubscriptionPlanService(),
		applicationRepo:         repository.NewApplications(),
		subscriptionRepo:        repository.NewSubscriptions(),
		subscriptionPlanRepo:    repository.NewSubscriptionPlans(),
	}
}

func (s *Service) ListEnterprisePlans(ctx context.Context) ([]*serialize.SubscriptionPlanResponse, apierror.Error) {
	subscriptionPlans, err := s.subscriptionPlanRepo.FindAllByIDPrefix(ctx, s.db, EnterprisePlanIDPrefix)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializableSubscriptionPlans, err := s.subscriptionPlanService.ConvertAllToSerializable(ctx, s.db, subscriptionPlans)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]*serialize.SubscriptionPlanResponse, len(serializableSubscriptionPlans))
	for i, serializableSubscriptionPlan := range serializableSubscriptionPlans {
		response[i] = serialize.SubscriptionPlan(serializableSubscriptionPlan)
	}
	return response, nil
}

type CreateEnterprisePlanParams struct {
	Customer       string   `json:"customer" validate:"required"`
	BasePrice      int64    `json:"base_price" validate:"required,min=0"`
	FreeMAUs       int64    `json:"free_maus" validate:"required,min=0"`
	FreeMAOs       int64    `json:"free_maos" validate:"required,min=0"`
	ApplicationIDs []string `json:"application_ids"`
}

func (s *Service) CreateEnterprisePlan(ctx context.Context, params CreateEnterprisePlanParams) (*serialize.SubscriptionPlanResponse, apierror.Error) {
	if err := s.validator.Struct(params); err != nil {
		return nil, apierror.FormValidationFailed(err)
	}

	var subscriptionPlanSerializable *serializable.SubscriptionPlan
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		newSubscriptionPlan, err := s.createSubscriptionPlan(ctx, tx, params)
		if err != nil {
			return true, err
		}
		subscriptionPlanSerializable, err = s.subscriptionPlanService.ConvertToSerializable(ctx, tx, newSubscriptionPlan)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.SubscriptionPlan(subscriptionPlanSerializable), nil
}

func (s *Service) createSubscriptionPlan(ctx context.Context, exec database.Executor, params CreateEnterprisePlanParams) (*model.SubscriptionPlan, error) {
	id := s.toEnterpriseProductID(params.Customer)

	subscriptionPlan, err := s.subscriptionPlanRepo.QueryByID(ctx, exec, id)
	if err != nil {
		return nil, err
	} else if subscriptionPlan != nil {
		return nil, apierror.CustomPlanAlreadyExists()
	}

	metadata := map[string]string{
		"id":                            id,
		"base_plan":                     cenv.Get(cenv.ClerkGodPlanID),
		"features":                      "[]",
		"description_html":              "Enterprise plan",
		"monthly_user_limit":            "0",
		"monthly_organization_limit":    "0",
		"organization_membership_limit": "0",
		"scope":                         constants.ApplicationResource,
		"sync":                          "true",
		"visible":                       "false",
		"visible_to_application_ids":    fmt.Sprintf("[\"%s\"]", strings.Join(params.ApplicationIDs, "\", \"")),
	}

	stripeSecretKey := cenv.Get(cenv.StripeSecretKey)
	stripeProduct, err := s.paymentProvider.CreateProduct(
		stripeSecretKey,
		&stripe.ProductParams{
			Name: stripe.String(fmt.Sprintf("Enterprise - %s", params.Customer)),
			Params: stripe.Params{
				Metadata: metadata,
			},
		})
	if err != nil {
		return nil, err
	}

	// Base Fee
	baseFeePrice, err := s.paymentProvider.CreatePrice(
		stripeSecretKey,
		&stripe.PriceParams{
			Active:      stripe.Bool(true),
			Nickname:    stripe.String("Base Price"),
			Product:     stripe.String(stripeProduct.ID),
			Recurring:   &stripe.PriceRecurringParams{Interval: stripe.String(string(stripe.PriceRecurringIntervalMonth))},
			Currency:    stripe.String(string(stripe.CurrencyUSD)),
			TaxBehavior: stripe.String(string(stripe.PriceTaxBehaviorExclusive)),
			UnitAmount:  stripe.Int64(params.BasePrice),
		})
	if err != nil {
		return nil, err
	}

	stripeProduct, err = s.paymentProvider.UpdateProduct(stripeSecretKey, stripeProduct.ID, &stripe.ProductParams{
		DefaultPrice: stripe.String(baseFeePrice.ID),
	})
	if err != nil {
		return nil, err
	}

	// MAUs
	mauPriceParams := stripePriceParams("MAUs", stripeProduct.ID, billing.PriceTypes.MAU, stripe.PriceRecurringAggregateUsageMax)
	mauPriceParams.BillingScheme = stripe.String(string(stripe.PriceBillingSchemeTiered))
	mauPriceParams.TiersMode = stripe.String(string(stripe.PriceTiersModeGraduated))
	mauPriceParams.Tiers = []*stripe.PriceTierParams{
		{
			UnitAmountDecimal: stripe.Float64(0),
			UpTo:              stripe.Int64(params.FreeMAUs),
		},
		{
			UnitAmountDecimal: stripe.Float64(2), // $0.02
			UpTo:              stripe.Int64(100_000),
		},
		{
			UnitAmountDecimal: stripe.Float64(1.5), // $0.015
			UpTo:              stripe.Int64(1_000_000),
		},
		{
			UnitAmountDecimal: stripe.Float64(1), // $0.01
			UpToInf:           stripe.Bool(true),
		},
	}
	_, err = s.paymentProvider.CreatePrice(stripeSecretKey, mauPriceParams)
	if err != nil {
		return nil, err
	}

	// MAOs
	maoPriceParams := stripePriceParams("MAOs", stripeProduct.ID, billing.PriceTypes.MAO, stripe.PriceRecurringAggregateUsageMax)
	maoPriceParams.BillingScheme = stripe.String(string(stripe.PriceBillingSchemeTiered))
	maoPriceParams.TiersMode = stripe.String(string(stripe.PriceTiersModeGraduated))
	maoPriceParams.Tiers = []*stripe.PriceTierParams{
		{
			UnitAmount: stripe.Int64(0),
			UpTo:       stripe.Int64(params.FreeMAOs),
		},
		{
			UnitAmount: stripe.Int64(100), // $1.00
			UpToInf:    stripe.Bool(true),
		},
	}
	_, err = s.paymentProvider.CreatePrice(stripeSecretKey, maoPriceParams)
	if err != nil {
		return nil, err
	}

	// SMS Tier A
	err = s.createUnitBasedPrice(stripeSecretKey, "SMS Tier A", stripeProduct.ID, 1, billing.PriceTypes.SMSMessagesTierA, stripe.PriceRecurringAggregateUsageLastDuringPeriod)
	if err != nil {
		return nil, err
	}

	// SMS Tier B
	err = s.createUnitBasedPrice(stripeSecretKey, "SMS Tier B", stripeProduct.ID, 5, billing.PriceTypes.SMSMessagesTierB, stripe.PriceRecurringAggregateUsageLastDuringPeriod)
	if err != nil {
		return nil, err
	}

	// SMS Tier C
	err = s.createUnitBasedPrice(stripeSecretKey, "SMS Tier C", stripeProduct.ID, 10, billing.PriceTypes.SMSMessagesTierC, stripe.PriceRecurringAggregateUsageLastDuringPeriod)
	if err != nil {
		return nil, err
	}

	// SMS Tier D
	err = s.createUnitBasedPrice(stripeSecretKey, "SMS Tier D", stripeProduct.ID, 20, billing.PriceTypes.SMSMessagesTierD, stripe.PriceRecurringAggregateUsageLastDuringPeriod)
	if err != nil {
		return nil, err
	}

	// SMS Tier E
	err = s.createUnitBasedPrice(stripeSecretKey, "SMS Tier E", stripeProduct.ID, 30, billing.PriceTypes.SMSMessagesTierE, stripe.PriceRecurringAggregateUsageLastDuringPeriod)
	if err != nil {
		return nil, err
	}

	// SMS Tier F
	err = s.createUnitBasedPrice(stripeSecretKey, "SMS Tier F", stripeProduct.ID, 75, billing.PriceTypes.SMSMessagesTierF, stripe.PriceRecurringAggregateUsageLastDuringPeriod)
	if err != nil {
		return nil, err
	}

	// Satellite Domains
	err = s.createUnitBasedPrice(stripeSecretKey, "Satellite Domains", stripeProduct.ID, 1000, billing.PriceTypes.Domains, stripe.PriceRecurringAggregateUsageMax)
	if err != nil {
		return nil, err
	}

	// SAML Connections
	err = s.createUnitBasedPrice(stripeSecretKey, "SAML Connections", stripeProduct.ID, 5000, billing.PriceTypes.SAMLConnections, stripe.PriceRecurringAggregateUsageMax)
	if err != nil {
		return nil, err
	}

	subscriptionPlan = &model.SubscriptionPlan{
		SubscriptionPlan: &sqbmodel.SubscriptionPlan{
			ID:                      metadata["id"],
			Title:                   stripeProduct.Name,
			MonthlyUserLimit:        0,
			DescriptionHTML:         null.StringFrom(stripeProduct.Description),
			StripeProductID:         null.StringFrom(stripeProduct.ID),
			BasePlan:                null.StringFrom(metadata["base_plan"]),
			Scope:                   constants.ApplicationResource,
			VisibleToApplicationIds: params.ApplicationIDs,
		},
	}
	err = s.subscriptionPlanRepo.Insert(ctx, exec, subscriptionPlan)
	if err != nil {
		return nil, err
	}
	return subscriptionPlan, nil
}

func (s *Service) createUnitBasedPrice(
	stripeSecretKey string,
	name string,
	stripeProductID string,
	unitAmountInCents int64,
	metric string,
	usageAggregationStrategy stripe.PriceRecurringAggregateUsage,
) error {
	params := stripePriceParams(name, stripeProductID, metric, usageAggregationStrategy)
	params.UnitAmount = stripe.Int64(unitAmountInCents)
	_, err := s.paymentProvider.CreatePrice(stripeSecretKey, params)
	return err
}

func stripePriceParams(name, stripeProductID, metric string, usageAggregationStrategy stripe.PriceRecurringAggregateUsage) *stripe.PriceParams {
	return &stripe.PriceParams{
		Active:   stripe.Bool(true),
		Nickname: stripe.String(name),
		Product:  stripe.String(stripeProductID),
		Recurring: &stripe.PriceRecurringParams{
			Interval:       stripe.String(string(stripe.PriceRecurringIntervalMonth)),
			UsageType:      stripe.String(string(stripe.PriceRecurringUsageTypeMetered)),
			AggregateUsage: stripe.String(string(usageAggregationStrategy)),
		},
		Currency:    stripe.String(string(stripe.CurrencyUSD)),
		TaxBehavior: stripe.String(string(stripe.PriceTaxBehaviorExclusive)),
		Params: stripe.Params{
			Metadata: map[string]string{
				"metric": metric,
			},
		},
	}
}

func (s *Service) toEnterpriseProductID(customer string) string {
	customer = strings.ToLower(customer)
	customer = strings.ReplaceAll(customer, " ", "_")
	now := s.clock.Now().UTC()
	year := now.Year()
	month := now.Month()

	return fmt.Sprintf("%s%s_%d_%d", EnterprisePlanIDPrefix, customer, year, month)
}

type AssignToApplicationsParams struct {
	SubscriptionPlanID string   `json:"-"`
	ApplicationIDs     []string `json:"application_ids"`
}

func (s *Service) AssignToApplications(ctx context.Context, params AssignToApplicationsParams) (*serialize.SubscriptionPlanResponse, apierror.Error) {
	subscriptionPlan, err := s.subscriptionPlanRepo.QueryByID(ctx, s.db, params.SubscriptionPlanID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if subscriptionPlan == nil {
		return nil, apierror.ResourceNotFound()
	}

	var serializableSubscriptionPlan *serializable.SubscriptionPlan
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		subscriptionPlan.VisibleToApplicationIds = append(subscriptionPlan.VisibleToApplicationIds, params.ApplicationIDs...)
		err := s.subscriptionPlanRepo.Update(ctx, tx, subscriptionPlan, sqbmodel.SubscriptionPlanColumns.VisibleToApplicationIds)
		if err != nil {
			return true, err
		}

		serializableSubscriptionPlan, err = s.subscriptionPlanService.ConvertToSerializable(ctx, tx, subscriptionPlan)
		if err != nil {
			return true, err
		}

		visibleToApplicationIDsJSON, err := json.Marshal(subscriptionPlan.VisibleToApplicationIds)
		if err != nil {
			return true, err
		}
		_, err = s.paymentProvider.UpdateProduct(cenv.Get(cenv.StripeSecretKey), subscriptionPlan.StripeProductID.String, &stripe.ProductParams{
			Params: stripe.Params{
				Metadata: map[string]string{
					"visible_to_application_ids": string(visibleToApplicationIDsJSON),
				},
			},
		})
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}
	return serialize.SubscriptionPlan(serializableSubscriptionPlan), nil
}

func (s *Service) ListApplicationsWithPendingTrials(ctx context.Context) ([]*serialize.TrialResponse, apierror.Error) {
	applicationsWithSubscriptions, err := s.applicationRepo.FindAllWithSubscriptionTrialDays(ctx, s.db)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]*serialize.TrialResponse, len(applicationsWithSubscriptions))
	for i, applicationWithSubscription := range applicationsWithSubscriptions {
		response[i] = serialize.Trial(applicationWithSubscription.Application, applicationWithSubscription.Subscription)
	}
	return response, nil
}

type SetApplicationTrialParams struct {
	ApplicationID string `json:"-"`
	Days          int    `json:"days"`
}

func (s *Service) SetTrialForApplication(ctx context.Context, params SetApplicationTrialParams) (*serialize.TrialResponse, apierror.Error) {
	if params.Days < 0 {
		return nil, apierror.FormInvalidParameterValue("days", strconv.Itoa(params.Days))
	}

	application, err := s.applicationRepo.QueryByID(ctx, s.db, params.ApplicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if application == nil {
		return nil, apierror.ResourceNotFound()
	}

	subscription, err := s.subscriptionRepo.FindByResourceID(ctx, s.db, application.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	subscription.TrialPeriodDays = params.Days
	if err := s.subscriptionRepo.UpdateTrialPeriodDays(ctx, s.db, subscription); err != nil {
		return nil, apierror.Unexpected(err)
	}
	return serialize.Trial(application, subscription), nil
}
