package organizations

import (
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"time"

	"clerk/api/apierror"
	dapiserialize "clerk/api/dapi/serialize"
	"clerk/api/dapi/v1/subscriptions"
	"clerk/api/serialize"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/api/shared/pricing"
	"clerk/model"
	"clerk/model/sqbmodel"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/params"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/organization"
	"github.com/clerk/clerk-sdk-go/v2/organizationmembership"
	"github.com/clerk/clerk-sdk-go/v2/user"
	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/stripe/stripe-go/v72"
)

type Service struct {
	clock           clockwork.Clock
	db              database.Database
	newSDKConfig    sdkutils.ConfigConstructor
	paymentProvider clerkbilling.PaymentProvider

	billingService      *pricing.Service
	organizationService *organizations.Service
	subscriptionService *subscriptions.Service

	applicationOwnershipRepo   *repository.ApplicationOwnerships
	authConfigRepo             *repository.AuthConfig
	organizationsRepo          *repository.Organization
	organizationMembershipRepo *repository.OrganizationMembership
	subscriptionRepo           *repository.Subscriptions
	subscriptionPlanRepo       *repository.SubscriptionPlans
	subscriptionProductRepo    *repository.SubscriptionProduct
}

func NewService(deps clerk.Deps, newSDKConfig sdkutils.ConfigConstructor, paymentProvider clerkbilling.PaymentProvider) *Service {
	return &Service{
		clock:                      deps.Clock(),
		db:                         deps.DB(),
		newSDKConfig:               newSDKConfig,
		paymentProvider:            clerkbilling.NewCachedPaymentProvider(deps.Clock(), deps.DB(), paymentProvider),
		billingService:             pricing.NewService(deps.DB(), deps.GueClient(), deps.Clock(), paymentProvider),
		organizationService:        organizations.NewService(deps),
		subscriptionService:        subscriptions.NewService(deps, paymentProvider),
		applicationOwnershipRepo:   repository.NewApplicationOwnerships(),
		authConfigRepo:             repository.NewAuthConfig(),
		organizationsRepo:          repository.NewOrganization(),
		organizationMembershipRepo: repository.NewOrganizationMembership(),
		subscriptionRepo:           repository.NewSubscriptions(),
		subscriptionPlanRepo:       repository.NewSubscriptionPlans(),
		subscriptionProductRepo:    repository.NewSubscriptionProduct(),
	}
}

type ListParams struct {
	InstanceID string `validate:"required"`
	Limit      string `validate:"omitempty,numeric,gte=1,lte=500"`
	Offset     string `validate:"omitempty,numeric,gte=0"`
	Query      string
	UserIDs    []string
	OrderBy    *string
}

// List returns a list of organizations along with their total number
// of members. Results can be paginated.
func (s *Service) List(ctx context.Context, params ListParams, paginationParams pagination.Params) (*sdk.OrganizationList, apierror.Error) {
	sdkParams := &organization.ListParams{
		UserIDs:             params.UserIDs,
		IncludeMembersCount: sdk.Bool(true),
		Query:               sdk.String(params.Query),
		OrderBy:             params.OrderBy,
	}
	sdkParams.Limit = sdk.Int64(int64(paginationParams.Limit))
	sdkParams.Offset = sdk.Int64(int64(paginationParams.Offset))

	sdkClient, apiErr := s.newOrganizationSDKClientForInstance(ctx, params.InstanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() (*sdk.OrganizationList, apierror.Error) {
		organizationsResponse, err := sdkClient.List(ctx, sdkParams)
		return organizationsResponse, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

type CreateOrganizationParams struct {
	organization.CreateParams
	InstanceID string
}

func (s *Service) Create(ctx context.Context, params CreateOrganizationParams) (*sdk.Organization, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationSDKClientForInstance(ctx, params.InstanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	organizationsResponse, err := sdkClient.Create(ctx, &params.CreateParams)
	return organizationsResponse, sdkutils.ToAPIError(err)
}

// Read returns details for a single organization
func (s *Service) Read(ctx context.Context, instanceID, organizationIDorSlug string) (*sdk.Organization, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	return sdkutils.WithRetry(func() (*sdk.Organization, apierror.Error) {
		organizationResponse, err := sdkClient.Get(ctx, organizationIDorSlug)
		return organizationResponse, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
}

type updateParams struct {
	Name                  *string         `json:"name,omitempty"`
	Slug                  *string         `json:"slug,omitempty"`
	MaxAllowedMemberships *int64          `json:"max_allowed_memberships,omitempty" validate:"omitempty,numeric,gte=0"`
	AdminDeleteEnabled    *bool           `json:"admin_delete_enabled,omitempty"`
	PublicMetadata        json.RawMessage `json:"public_metadata,omitempty"`
	PrivateMetadata       json.RawMessage `json:"private_metadata,omitempty"`
}

func (params *updateParams) validate() apierror.Error {
	if params.Name != nil && *params.Name == "" {
		return apierror.FormMissingParameter("name")
	}
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}

	return nil
}

func (params *updateParams) toSDKParams() *organization.UpdateParams {
	return &organization.UpdateParams{
		Name:                  params.Name,
		Slug:                  params.Slug,
		MaxAllowedMemberships: params.MaxAllowedMemberships,
		AdminDeleteEnabled:    params.AdminDeleteEnabled,
		PublicMetadata:        sdk.JSONRawMessage(params.PublicMetadata),
		PrivateMetadata:       sdk.JSONRawMessage(params.PrivateMetadata),
	}
}

func (s *Service) Update(ctx context.Context, params *updateParams, organizationID, instanceID string) (*sdk.Organization, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	apiErr = params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	organizationResponse, err := sdkClient.Update(ctx, organizationID, params.toSDKParams())
	return organizationResponse, sdkutils.ToAPIError(err)
}

func (s *Service) Delete(ctx context.Context, organizationID, instanceID string) (*sdk.DeletedResource, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	organizationResponse, err := sdkClient.Delete(ctx, organizationID)
	return organizationResponse, sdkutils.ToAPIError(err)
}

func (s *Service) UpdateMetadata(ctx context.Context, organizationID, instanceID string, params organization.UpdateMetadataParams) (*sdk.Organization, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	organizationResponse, err := sdkClient.UpdateMetadata(ctx, organizationID, &params)
	return organizationResponse, sdkutils.ToAPIError(err)
}

type updateLogoParams struct {
	organizationID string
	instanceID     string
	file           multipart.File
	filename       string
}

func (s *Service) UpdateLogo(ctx context.Context, params updateLogoParams) (*sdk.Organization, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationSDKClientForInstance(ctx, params.instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	activeSession, _ := sdkutils.GetActiveSession(ctx)

	org, err := s.organizationsRepo.QueryByID(ctx, s.db, params.organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if org == nil {
		return nil, apierror.OrganizationNotFound()
	}

	res, err := sdkClient.UpdateLogo(ctx, params.organizationID, &organization.UpdateLogoParams{
		File:           params.file,
		UploaderUserID: sdk.String(activeSession.Subject),
	})
	return res, sdkutils.ToAPIError(err)
}

type deleteLogoParams struct {
	organizationID string
	instanceID     string
}

func (s *Service) DeleteLogo(ctx context.Context, params deleteLogoParams) (*sdk.Organization, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationSDKClientForInstance(ctx, params.instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	res, err := sdkClient.DeleteLogo(ctx, params.organizationID)
	return res, sdkutils.ToAPIError(err)
}

// CheckOrganizationsEnabled returns an error if the organizations feature
// is not enabled for the instance.
func (s *Service) CheckOrganizationsEnabled(ctx context.Context, instanceID string) apierror.Error {
	authConfig, err := s.authConfigRepo.QueryByInstanceActiveAuthConfigID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if authConfig == nil || !authConfig.IsOrganizationsEnabled() {
		return apierror.OrganizationNotEnabledInInstance()
	}
	return nil
}

// CheckOrganizationAdmin checks whether the requesting user is an admin on
// the given organization. If they are not, it returns an error.
func (s *Service) CheckOrganizationAdmin(ctx context.Context, organizationID string) apierror.Error {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	activeMembership, err := s.organizationMembershipRepo.QueryByOrganizationAndUser(ctx, s.db, organizationID, activeSession.Subject)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if activeMembership == nil || !activeMembership.HasRole(constants.RoleAdmin) {
		return apierror.NotAnAdminInOrganization()
	}
	return nil
}

// RefreshPaymentStatus checks the payment status for a particular checkout session
// To be called by the UI after the redirect back to Clerk from the payment provider
func (s *Service) RefreshPaymentStatus(ctx context.Context, organizationID string,
	refreshPaymentStatusParams params.RefreshPaymentStatusParams,
) (*serialize.SubscriptionResponse, apierror.Error) {
	organization, err := s.organizationsRepo.FindByID(ctx, s.db, organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var subscription *model.Subscription
	var currentPlan *model.SubscriptionPlan
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		subscription, err = s.subscriptionRepo.FindByResourceIDForUpdate(ctx, tx, organizationID)
		if err != nil {
			return true, err
		}

		if refreshPaymentStatusParams.CheckoutSessionID != "" &&
			refreshPaymentStatusParams.CheckoutSessionID != subscription.StripeCheckoutSessionID.String {
			return true, apierror.CheckoutSessionMismatch(organizationID, refreshPaymentStatusParams.CheckoutSessionID)
		}

		if refreshPaymentStatusParams.CheckoutSessionID == "" {
			return false, nil
		}

		checkoutSession, err := s.paymentProvider.FetchCheckoutSession(refreshPaymentStatusParams.CheckoutSessionID)
		if err != nil {
			return true, err
		}

		apiErr := s.billingService.CheckoutSessionCompleted(ctx, tx, pricing.CheckoutSessionCompletedParams{
			CheckoutSession: *checkoutSession,
			Resource: pricing.BillableResource{
				ID:                  organization.ID,
				Name:                organization.Name,
				Type:                constants.OrganizationResource,
				UnsupportedFeatures: pricing.NoUnsupportedFeatures,
			},
			Subscription: subscription,
		})
		if apiErr != nil {
			return true, apiErr
		}

		subscriptionPlans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, subscription.ID)
		if err != nil {
			return true, err
		}
		organization.MaxAllowedMemberships = model.MaxAllowedOrganizationMemberships(subscriptionPlans)
		err = s.organizationsRepo.UpdateMaxAllowedMemberships(ctx, tx, organization)
		if err != nil {
			return true, err
		}

		currentPlan = clerkbilling.DetectCurrentPlan(subscriptionPlans)

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return serialize.Subscription(subscription, currentPlan), nil
}

// ReadSubscription returns the subscription for the given organization.
func (s *Service) ReadSubscription(ctx context.Context, organizationID string) (*serialize.SubscriptionResponse, apierror.Error) {
	subscription, err := s.subscriptionRepo.QueryByResourceID(ctx, s.db, organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	subscriptionPlans := make([]*model.SubscriptionPlan, 0)
	if subscription == nil {
		subscription = &model.Subscription{
			Subscription: &sqbmodel.Subscription{
				ResourceID:   organizationID,
				ResourceType: constants.OrganizationResource,
			},
		}

		// the webhook has not been triggered yet, find the first available and free for organizations and
		// use that one instead.
		subscriptionPlan, err := s.subscriptionPlanRepo.FindFirstAvailableAndFreeByResourceType(ctx, s.db, constants.OrganizationResource)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		subscriptionPlans = append(subscriptionPlans, subscriptionPlan)
	} else {
		subscriptionPlans, err = s.subscriptionPlanRepo.FindAllBySubscription(ctx, s.db, subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	currentPlan := clerkbilling.DetectCurrentPlan(subscriptionPlans)
	if currentPlan == nil {
		return nil, apierror.Unexpected(fmt.Errorf("no plan for subscription %s", subscription.ID))
	}

	return serialize.Subscription(
		subscription,
		currentPlan,
		serialize.WithOrganizationMembershipLimit(model.MaxAllowedOrganizationMemberships(subscriptionPlans)),
	), nil
}

func (s *Service) ListPlans(ctx context.Context, organizationID string) ([]*serialize.SubscriptionPlanWithPricesResponse, apierror.Error) {
	subscription, err := s.subscriptionRepo.QueryByResourceID(ctx, s.db, organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if subscription == nil {
		return nil, apierror.ResourceNotFound()
	}

	subscriptionProducts, err := s.subscriptionProductRepo.FindAllBySubscriptionID(ctx, s.db, subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	subscriptionPlanIDs := set.New[string]()
	for _, subscriptionProduct := range subscriptionProducts {
		subscriptionPlanIDs.Insert(subscriptionProduct.SubscriptionPlanID)
	}
	plans, err := s.subscriptionPlanRepo.FindAvailableForResource(ctx, s.db, subscriptionPlanIDs.Array(), organizationID, constants.OrganizationResource)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var activePlan *model.SubscriptionPlan
	for _, plan := range plans {
		if !plan.IsAddon && subscriptionPlanIDs.Contains(plan.ID) {
			activePlan = plan
			break
		}
	}

	response := make([]*serialize.SubscriptionPlanWithPricesResponse, len(plans))
	for i := range plans {
		action := determineAction(activePlan, plans[i])
		response[i] = serialize.SubscriptionPlanWithPrices(model.NewSubscriptionPlanWithPrices(plans[i], []*model.SubscriptionPrice{}), serialize.WithAction(action))
	}

	return response, nil
}

type ListMembershipsParams struct {
	Query          *string
	OrganizationID string
	Roles          []string
	OrderBy        *string
}

type Identifiers struct {
	EmailAddress string `json:"email_address"`
	PhoneNumber  string `json:"phone_number"`
	Username     string `json:"username"`
}
type MembershipWithIdentifiers struct {
	sdk.OrganizationMembership
	Identifiers Identifiers `json:"identifiers"`
}

func (s *Service) getPrimaryAddress(user sdk.User) string {
	if user.PrimaryEmailAddressID == nil {
		return ""
	}
	for _, currentEmail := range user.EmailAddresses {
		if currentEmail.ID == *user.PrimaryEmailAddressID {
			return currentEmail.EmailAddress
		}
	}
	return ""
}

func (s *Service) getPrimaryPhoneNumber(user sdk.User) string {
	if user.PrimaryPhoneNumberID == nil {
		return ""
	}

	if user.PrimaryPhoneNumberID != nil {
		for _, currentPhoneNumber := range user.PhoneNumbers {
			if currentPhoneNumber.ID == *user.PrimaryPhoneNumberID {
				return currentPhoneNumber.PhoneNumber
			}
		}
	}
	return ""
}

func (s *Service) getUsername(user sdk.User) string {
	if user.Username == nil {
		return ""
	}
	return *user.Username
}

func (s *Service) ListMemberships(
	ctx context.Context,
	instanceID string,
	params ListMembershipsParams,
	paginationParams pagination.Params,
) (*serialize.PaginatedResponse, apierror.Error) {
	sdkConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	sdkParams := &organizationmembership.ListParams{
		OrganizationID: params.OrganizationID,
		Roles:          params.Roles,
		Query:          params.Query,
		OrderBy:        params.OrderBy,
	}
	sdkParams.Limit = sdk.Int64(int64(paginationParams.Limit))
	sdkParams.Offset = sdk.Int64(int64(paginationParams.Offset))
	orgMembershipsClient := organizationmembership.NewClient(sdkConfig)

	list, apiErr := sdkutils.WithRetry(func() (*sdk.OrganizationMembershipList, apierror.Error) {
		response, err := orgMembershipsClient.List(ctx, sdkParams)
		return response, sdkutils.ToAPIError(err)
	}, sdkutils.RetryConfig{
		MaxAttempts: 3,
		Delay:       60 * time.Millisecond,
	})
	if apiErr != nil {
		return nil, apiErr
	}

	// find all members user_ids
	userIDs := make([]string, list.TotalCount)
	for _, member := range list.OrganizationMemberships {
		userIDs = append(userIDs, member.PublicUserData.UserID)
	}

	listUsersParams := user.ListParams{
		UserIDs: userIDs,
	}
	listUsersParams.Limit = sdk.Int64(int64(paginationParams.Limit))
	usersResponse, err := user.NewClient(sdkConfig).List(ctx, &listUsersParams)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}

	// create a map with user ids as key and the user data as value
	usersResponseMap := make(map[string]sdk.User)
	for _, user := range usersResponse.Users {
		usersResponseMap[user.ID] = *user
	}

	var membershipsWithIdentifiers []interface{}

	for _, member := range list.OrganizationMemberships {
		user, ok := usersResponseMap[member.PublicUserData.UserID]
		if !ok {
			continue
		}

		membership := MembershipWithIdentifiers{
			OrganizationMembership: *member,
			Identifiers: Identifiers{
				EmailAddress: s.getPrimaryAddress(user),
				PhoneNumber:  s.getPrimaryPhoneNumber(user),
				Username:     s.getUsername(user),
			},
		}

		membershipsWithIdentifiers = append(membershipsWithIdentifiers, membership)
	}

	return serialize.Paginated(membershipsWithIdentifiers, list.TotalCount), sdkutils.ToAPIError(err)
}

type CreateMembershipParams struct {
	organizationmembership.CreateParams
	InstanceID string
}

func (s *Service) CreateMembership(ctx context.Context, params CreateMembershipParams) (*sdk.OrganizationMembership, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationMembershipSDKClientForInstance(ctx, params.InstanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	membershipResponse, err := sdkClient.Create(ctx, &params.CreateParams)
	return membershipResponse, sdkutils.ToAPIError(err)
}

func (s *Service) UpdateMembership(ctx context.Context, instanceID string, params *organizationmembership.UpdateParams) (*sdk.OrganizationMembership, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationMembershipSDKClientForInstance(ctx, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	membershipResponse, err := sdkClient.Update(ctx, params)
	return membershipResponse, sdkutils.ToAPIError(err)
}

type DeleteMembershipParams struct {
	OrganizationID string
	InstanceID     string
	UserID         string
}

func (s *Service) DeleteMembership(ctx context.Context, params DeleteMembershipParams) (*sdk.OrganizationMembership, apierror.Error) {
	sdkClient, apiErr := s.newOrganizationMembershipSDKClientForInstance(ctx, params.InstanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	membershipResponse, err := sdkClient.Delete(ctx, &organizationmembership.DeleteParams{
		OrganizationID: params.OrganizationID,
		UserID:         params.UserID,
	})
	return membershipResponse, sdkutils.ToAPIError(err)
}

func (s *Service) CurrentSubscription(ctx context.Context, organizationID string) (*dapiserialize.CurrentOrganizationSubscriptionResponse, apierror.Error) {
	applicationOwnerships, err := s.applicationOwnershipRepo.FindAllByOwnerID(ctx, s.db, organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// find usage details for organization members
	organizationSubscription, err := s.subscriptionRepo.QueryByResourceID(ctx, s.db, organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if organizationSubscription == nil {
		return nil, apierror.ResourceNotFound()
	}

	currentBillingCycle := organizationSubscription.GetCurrentBillingCycle(s.clock.Now().UTC())

	var totalCredit int64
	var memberUsage *model.Usage
	if organizationSubscription.StripeSubscriptionID.Valid {
		nextInvoice, err := s.paymentProvider.FetchNextInvoice(ctx, organizationSubscription.StripeSubscriptionID.String)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		if credit, creditExists := nextInvoice.Customer.InvoiceCreditBalance[string(stripe.CurrencyUSD)]; creditExists {
			totalCredit = credit
		}

		// get usage from Stripe
		memberUsage, err = s.billableUsageFromStripe(ctx, nextInvoice, organizationSubscription)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	} else {
		// free tier
		memberUsage, err = s.freeUsage(ctx, organizationSubscription)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	// find usage details for applications of that organization
	applicationSubscriptions := make([]*dapiserialize.CurrentApplicationSubscriptionResponse, len(applicationOwnerships))
	for i, ownership := range applicationOwnerships {
		applicationSubscriptions[i], err = s.subscriptionService.CurrentApplicationSubscription(ctx, s.db, ownership.ApplicationID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}
	return dapiserialize.CurrentOrganizationSubscription(
		totalCredit,
		applicationSubscriptions,
		dapiserialize.WithMemberUsage(organizationSubscription, currentBillingCycle, memberUsage),
	), nil
}

func (s *Service) freeUsage(ctx context.Context, organizationSubscription *model.Subscription) (*model.Usage, error) {
	organizationPlan, err := s.subscriptionPlanRepo.FindBySubscription(ctx, s.db, organizationSubscription.ID)
	if err != nil {
		return nil, err
	}

	currentMembers, err := s.organizationMembershipRepo.CountByOrganization(ctx, s.db, organizationSubscription.ResourceID)
	if err != nil {
		return nil, err
	}

	return &model.Usage{
		Metric:     "seats",
		AmountDue:  0,
		TotalUnits: currentMembers,
		HardLimit:  int64(organizationPlan.OrganizationMembershipLimit),
	}, nil
}

func (s *Service) billableUsageFromStripe(ctx context.Context, nextInvoice *stripe.Invoice, organizationSubscription *model.Subscription) (*model.Usage, error) {
	usagesPerMetric, err := s.subscriptionService.UsagesFromInvoice(ctx, s.db, organizationSubscription, nextInvoice)
	if err != nil {
		return nil, err
	}

	memberUsage, usageExists := usagesPerMetric[clerkbilling.PriceTypes.Seats]
	if usageExists {
		return memberUsage, nil
	}
	return s.freeUsage(ctx, organizationSubscription)
}

// determineAction returns the type of action required to go from the active plan to the given one.
// * `upgrade` when going from a lower plan to a higher one
// * `downgrade` when going from a higher to a lower plan
// * `switch` when going to a similar plan
func determineAction(activePlan, newPlan *model.SubscriptionPlan) string {
	if activePlan.ID == newPlan.ID {
		return ""
	}

	if activePlan.OrganizationMembershipLimit == newPlan.OrganizationMembershipLimit {
		return "switch"
	}
	if activePlan.OrganizationMembershipLimit == 0 {
		return "downgrade"
	} else if newPlan.OrganizationMembershipLimit == 0 {
		return "upgrade"
	} else if activePlan.OrganizationMembershipLimit > newPlan.OrganizationMembershipLimit {
		return "downgrade"
	}

	return "upgrade"
}

func (s *Service) newOrganizationSDKClientForInstance(ctx context.Context, instanceID string) (*organization.Client, apierror.Error) {
	config, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return organization.NewClient(config), nil
}

func (s *Service) newOrganizationMembershipSDKClientForInstance(ctx context.Context, instanceID string) (*organizationmembership.Client, apierror.Error) {
	config, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}
	return organizationmembership.NewClient(config), nil
}
