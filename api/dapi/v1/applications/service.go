package applications

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"clerk/api/apierror"
	dapiserialize "clerk/api/dapi/serialize"
	"clerk/api/dapi/v1/authorization"
	"clerk/api/dapi/v1/pricing"
	"clerk/api/dapi/v1/subscriptions"
	"clerk/api/serialize"
	"clerk/api/shared/applications"
	"clerk/api/shared/domains"
	"clerk/api/shared/images"
	shpricing "clerk/api/shared/pricing"
	"clerk/model"
	"clerk/model/sqbmodel"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	ctxvalidator "clerk/pkg/ctx/validator"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/externalapis/segment"
	"clerk/pkg/externalapis/slack"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/generate"
	"clerk/pkg/jobs"
	"clerk/pkg/oauth/provider"
	"clerk/pkg/params"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/segment/dapi"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/set"
	clerkstrings "clerk/pkg/strings"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"
	"clerk/utils/validate"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/stripe/stripe-go/v72"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

const (
	addOnActionEnabled  = "enabled"
	addOnActionDisabled = "disabled"
)

type Service struct {
	clock           clockwork.Clock
	db              database.Database
	gueClient       *gue.Client
	paymentProvider clerkbilling.PaymentProvider

	// Services
	applicationDeleter   *applications.Deleter
	applicationService   *applications.Service
	imageService         *images.Service
	pricingService       *pricing.Service
	sharedDomainService  *domains.Service
	sharedPricingService *shpricing.Service
	subscriptionService  *subscriptions.Service

	// Repositories
	appRepo                    *repository.Applications
	applicationOwnershipRepo   *repository.ApplicationOwnerships
	billingAcctRepo            *repository.BillingAccounts
	displayConfigRepo          *repository.DisplayConfig
	domainRepo                 *repository.Domain
	imagesRepo                 *repository.Images
	instanceRepo               *repository.Instances
	integrationRepo            *repository.Integrations
	organizationMembershipRepo *repository.OrganizationMembership
	subscriptionRepo           *repository.Subscriptions
	subscriptionMetricsRepo    *repository.SubscriptionMetrics
	subscriptionPlanRepo       *repository.SubscriptionPlans
	subscriptionPriceRepo      *repository.SubscriptionPrices
	subscriptionProductsRepo   *repository.SubscriptionProduct

	// Clients
	svixClient        *svix.Client
	clerkImagesClient *clerkimages.Client
}

func NewService(
	deps clerk.Deps,
	svixClient *svix.Client,
	clerkImagesClient *clerkimages.Client,
	paymentProvider clerkbilling.PaymentProvider,
) *Service {
	return &Service{
		clock:             deps.Clock(),
		db:                deps.DB(),
		gueClient:         deps.GueClient(),
		svixClient:        svixClient,
		clerkImagesClient: clerkImagesClient,
		paymentProvider:   clerkbilling.NewCachedPaymentProvider(deps.Clock(), deps.DB(), paymentProvider),

		applicationDeleter:   applications.NewDeleter(deps),
		applicationService:   applications.NewService(),
		pricingService:       pricing.NewService(deps, paymentProvider),
		imageService:         images.NewService(deps.StorageClient()),
		sharedPricingService: shpricing.NewService(deps.DB(), deps.GueClient(), deps.Clock(), paymentProvider),
		sharedDomainService:  domains.NewService(deps),
		subscriptionService:  subscriptions.NewService(deps, paymentProvider),

		appRepo:                    repository.NewApplications(),
		applicationOwnershipRepo:   repository.NewApplicationOwnerships(),
		billingAcctRepo:            repository.NewBillingAccounts(),
		displayConfigRepo:          repository.NewDisplayConfig(),
		domainRepo:                 repository.NewDomain(),
		imagesRepo:                 repository.NewImages(),
		instanceRepo:               repository.NewInstances(),
		integrationRepo:            repository.NewIntegrations(),
		organizationMembershipRepo: repository.NewOrganizationMembership(),
		subscriptionRepo:           repository.NewSubscriptions(),
		subscriptionMetricsRepo:    repository.NewSubscriptionMetrics(),
		subscriptionPlanRepo:       repository.NewSubscriptionPlans(),
		subscriptionPriceRepo:      repository.NewSubscriptionPrices(),
		subscriptionProductsRepo:   repository.NewSubscriptionProduct(),
	}
}

// Create creates a new application for the current user
func (s *Service) Create(ctx context.Context, createApplicationSettings params.CreateApplicationSettings) (*serialize.MinimalApplicationResponse, apierror.Error) {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	if err := ctxvalidator.FromContext(ctx).Struct(createApplicationSettings); err != nil {
		return nil, apierror.FormValidationFailed(err)
	}

	// Custom validations that allow us to return a specific error
	if valid, reason := validate.NameForAbusePrevention(createApplicationSettings.Name); !valid {
		return nil, apierror.InvalidApplicationName(createApplicationSettings.Name, reason)
	}

	displayConfigOptions := model.DisplayConfigOpts{
		Color:          &createApplicationSettings.Color,
		ClerkJSVersion: model.DisplayConfigClass.DefaultClerkJSVersion(),
	}

	authConfigOptions := []func(*model.AuthConfig){
		generate.WithSingleSession(),
	}
	authConfigOptions = appendIdentifierOptions(createApplicationSettings.Identifiers, authConfigOptions)
	authConfigOptions = appendOAuthOptions(createApplicationSettings.OAuthProviders, authConfigOptions)
	authConfigOptions = appendWeb3Options(createApplicationSettings.Web3Providers, authConfigOptions)

	// auto-enable password if identification list is not empty
	// technically password is strictly necessary only for username
	// but enabling for all to keep existing behaviour
	if len(createApplicationSettings.Identifiers) > 0 {
		authConfigOptions = append(authConfigOptions, generate.WithPassword(true))
	}

	// Enable optional email attribute if the only identifier is username
	if len(createApplicationSettings.Identifiers) == 1 && createApplicationSettings.Identifiers[0] == constants.ITUsername {
		authConfigOptions = append(authConfigOptions, generate.WithEmailAddress(false, set.New(constants.VSEmailCode), false, set.New[string](), true))
	}

	freePlan, err := s.subscriptionPlanRepo.FindFirstAvailableAndFreeByResourceType(ctx, s.db, constants.ApplicationResource)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Create all development instances in test mode
	authConfigOptions = append(authConfigOptions, generate.WithTestMode())

	var application *model.Application
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		stack, err := generate.NewStack(ctx, txEmitter, s.gueClient, &generate.StackOptions{
			ApplicationName:   createApplicationSettings.Name,
			AuthConfigOptions: authConfigOptions,
			DisplayConfig:     displayConfigOptions,
			EnvironmentType:   constants.ETDevelopment,
			Plan:              freePlan,
			ResourceType:      constants.RTUser,
		})
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		application = stack.Application

		application.CreatorID = null.StringFrom(activeSession.Subject)
		err = s.appRepo.UpdateCreatorID(ctx, txEmitter, application)
		if err != nil {
			return true, err
		}

		ownership := &model.ApplicationOwnership{
			ApplicationOwnership: &sqbmodel.ApplicationOwnership{
				ApplicationID: application.ID,
			},
		}

		if activeSession.ActiveOrganizationID != "" {
			ownership.OrganizationID = null.StringFrom(activeSession.ActiveOrganizationID)
		} else {
			ownership.UserID = null.StringFrom(activeSession.Subject)
		}

		if err = s.applicationOwnershipRepo.Insert(ctx, txEmitter, ownership); err != nil {
			return true, err
		}

		// Push image settings to Clerk Img upon instance creation
		err = s.createAvatarSettings(ctx, txEmitter, stack.Instance.ID, stack.Instance.ActiveDisplayConfigID)

		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	dapi.EnqueueSegmentEvent(ctx, s.gueClient, dapi.SegmentParams{EventName: segment.APIDashboardApplicationCreated, UserID: activeSession.Subject, ApplicationID: application.ID})

	applicationSerializable, err := s.convertToSerializableMinimal(ctx, s.db, application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.MinimalApplication(ctx, applicationSerializable), nil
}

func (s *Service) Delete(ctx context.Context, appID string) apierror.Error {
	if cenv.IsEnabled(cenv.FlagPreventDeletionOfActiveProdInstance) {
		hasActiveProductionInstance, err := s.applicationService.HasRecentlyActiveProductionInstance(ctx, s.db, s.clock, appID)
		if err != nil {
			return apierror.Unexpected(err)
		}
		if hasActiveProductionInstance {
			return apierror.CannotDeleteActiveApplication()
		}
	}
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		err := s.applicationDeleter.SoftDelete(ctx, txEmitter, appID, s.clock.Now().UTC(), s.paymentProvider)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}
		return apierror.Unexpected(txErr)
	}
	return nil
}

func appendIdentifierOptions(identifiers []string, options []func(*model.AuthConfig)) []func(*model.AuthConfig) {
	for _, identifier := range identifiers {
		switch identifier {
		case constants.ITEmailAddress:
			options = append(options, generate.WithEmailAddress(true, set.New(constants.VSEmailCode), true, set.New(constants.VSEmailCode), true))
		case constants.ITPhoneNumber:
			options = append(options, generate.WithPhoneNumber(true, true, set.New(constants.VSPhoneCode), true, true))
		case constants.ITUsername:
			options = append(options, generate.WithUsername(true, true))
		}
	}
	return options
}

func appendOAuthOptions(providers []string, options []func(*model.AuthConfig)) []func(*model.AuthConfig) {
	for _, p := range providers {
		blockEmailSubaddresses := cenv.IsEnabled(cenv.FlagOAuthBlockEmailSubaddresses) && p == provider.GoogleID()
		options = append(options, generate.WithOAuth(p, false, true, blockEmailSubaddresses))
	}
	return options
}

func appendWeb3Options(providers []string, options []func(*model.AuthConfig)) []func(*model.AuthConfig) {
	for _, provider := range providers {
		// Before PSU, if Web3 was enabled, it was always marked as 'required'. But this
		// was not correct, because in reality Web3 is optional (i.e. you can sign up with
		// Web3 OR email). So in PSU we fixed this.
		required := !cenv.GetBool(cenv.ProgressiveSignUpOnNewApps)
		options = append(options, generate.WithWeb3(provider, required, true))
	}
	return options
}

func (s *Service) createAvatarSettings(ctx context.Context, tx database.Tx, instanceID string, displayConfigID string) error {
	displayConfig, err := s.displayConfigRepo.FindByID(ctx, tx, displayConfigID)
	if err != nil {
		return err
	}

	err = s.clerkImagesClient.CreateAvatarSettings(ctx, instanceID, clerkimages.CreateAvatarSettingsParams{
		User:         clerkimages.AvatarSettings(displayConfig.ImageSettings.User),
		Organization: clerkimages.AvatarSettings(displayConfig.ImageSettings.Organization),
	})

	// If image syncing fails, log and continue
	if err != nil {
		sentryclerk.CaptureException(ctx, fmt.Errorf("failed to sync image settings for (instance: %s, displayConfig: %s): %w", instanceID, displayConfigID, err))
		return nil
	}

	return err
}

func (s *Service) Update(ctx context.Context, appID string, applicationSettings params.UpdateApplicationSettings) (*serialize.ExtendedApplicationResponse, apierror.Error) {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	app, err := s.appRepo.FindByID(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if err = ctxvalidator.FromContext(ctx).Struct(applicationSettings); err != nil {
		return nil, apierror.FormValidationFailed(err)
	}

	// NOTE(2024-05-01, izaak): Not all existing application names pass this new validator,
	// and we want those existing customers to still be able to update other application
	// settings without requiring then to change their application name.
	// This only applies the new name validator if the name is being updated.
	if app.Name != applicationSettings.Name {
		// Custom validations that allow us to return a specific error
		if valid, reason := validate.NameForAbusePrevention(applicationSettings.Name); !valid {
			return nil, apierror.InvalidApplicationName(applicationSettings.Name, reason)
		}
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		app.Name = applicationSettings.Name
		err = s.appRepo.UpdateName(ctx, txEmitter, app)
		if err != nil {
			return true, err
		}

		if applicationSettings.CardBackgroundColor != "" {
			err := s.updateThemeColor(ctx, txEmitter, app.ID, applicationSettings.CardBackgroundColor)
			if err != nil {
				return true, err
			}
		}
		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return s.toResponse(ctx, app, activeSession.ActiveOrganizationRole)
}

// Update the theme's general color for all the application's active
// display configs.
func (s *Service) updateThemeColor(ctx context.Context, txEmitter database.TxEmitter, applicationID, color string) error {
	displayConfigs, err := s.displayConfigRepo.FindAllActiveByApplication(ctx, txEmitter, applicationID)
	if err != nil {
		return err
	}
	for _, dc := range displayConfigs {
		var theme model.Theme
		err := json.Unmarshal(dc.Theme, &theme)
		if err != nil {
			return err
		}
		theme.General.Color = color
		raw, err := json.Marshal(theme)
		if err != nil {
			return err
		}
		dc.Theme = raw
		err = s.displayConfigRepo.UpdateTheme(ctx, txEmitter, dc)
		if err != nil {
			return err
		}
	}
	return nil
}

type updateImageParams struct {
	applicationID  string
	application    *model.Application
	image          io.ReadCloser
	imagePublicURL string
}

func (s *Service) UpdateLogo(ctx context.Context, params updateImageParams) (*serialize.MinimalApplicationResponse, apierror.Error) {
	application, err := s.fetchApplication(ctx, s.db, params.applicationID)
	if err != nil {
		return nil, err
	}

	params.application = application
	params.imagePublicURL = application.LogoPublicURL.String

	return s.updateImage(ctx, params, s.updateAppLogo)
}

func (s *Service) DeleteLogo(ctx context.Context, applicationID string) (*serialize.MinimalApplicationResponse, apierror.Error) {
	application, err := s.appRepo.QueryByID(ctx, s.db, applicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if application == nil {
		return nil, apierror.ApplicationNotFound(applicationID)
	}
	if !application.LogoPublicURL.Valid {
		return nil, apierror.ImageNotFound()
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		err := jobs.CleanupImage(
			ctx,
			s.gueClient,
			jobs.CleanupImageArgs{
				PublicURL: application.LogoPublicURL.String,
			},
			jobs.WithTx(txEmitter),
		)
		if err != nil {
			return true, err
		}

		err = s.updateAppLogo(ctx, txEmitter, nil, application)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	serializable, err := s.convertToSerializableMinimal(ctx, s.db, application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return serialize.MinimalApplication(ctx, serializable), nil
}

func (s *Service) UpdateFavicon(ctx context.Context, params updateImageParams) (*serialize.MinimalApplicationResponse, apierror.Error) {
	application, err := s.fetchApplication(ctx, s.db, params.applicationID)
	if err != nil {
		return nil, err
	}

	params.application = application
	params.imagePublicURL = application.FaviconPublicURL.String
	return s.updateImage(ctx, params, s.updateAppFavicon)
}

type appImageUpdateFn func(context.Context, database.TxEmitter, *string, *model.Application) error

// Writes the logo_public_url to the database.
func (s *Service) updateAppLogo(
	ctx context.Context,
	txEmitter database.TxEmitter,
	imageURL *string,
	application *model.Application,
) error {
	application.LogoPublicURL = null.StringFromPtr(imageURL)
	return s.appRepo.UpdateLogoPublicURL(ctx, txEmitter, application)
}

// Writes the favicon_public_url to the database.
func (s *Service) updateAppFavicon(
	ctx context.Context,
	txEmitter database.TxEmitter,
	imageURL *string,
	application *model.Application,
) error {
	application.FaviconPublicURL = null.StringFromPtr(imageURL)
	return s.appRepo.UpdateFaviconPublicURL(ctx, txEmitter, application)
}

// Creates an image and updates the correct image type FK based on the provided
// appImageUpdateFn.
func (s *Service) updateImage(
	ctx context.Context,
	params updateImageParams,
	writer appImageUpdateFn,
) (*serialize.MinimalApplicationResponse, apierror.Error) {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		img, apiErr := s.imageService.Create(
			ctx,
			txEmitter,
			images.ImageParams{
				Prefix:             images.PrefixUploaded,
				Src:                params.image,
				UploaderUserID:     activeSession.Subject,
				UsedByResourceType: clerkstrings.ToPtr(constants.ApplicationResource),
			},
		)
		if apiErr != nil {
			return true, apiErr
		}

		if params.imagePublicURL != "" {
			err := jobs.CleanupImage(
				ctx,
				s.gueClient,
				jobs.CleanupImageArgs{
					PublicURL: params.imagePublicURL,
				},
				jobs.WithTx(txEmitter),
			)
			if err != nil {
				return true, err
			}
		}

		err := writer(ctx, txEmitter, &img.PublicURL, params.application)
		if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	serializable, err := s.convertToSerializableMinimal(ctx, s.db, params.application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return serialize.MinimalApplication(ctx, serializable), nil
}

// List returns all applications for user of active session
func (s *Service) List(ctx context.Context) ([]*serialize.ExtendedApplicationResponse, apierror.Error) {
	log.Debug(ctx, "applications/List: Listing...")
	activeSession, _ := sdkutils.GetActiveSession(ctx)
	log.Debug(ctx, "applications/List: activeSession: id=%s user=%s org=%s", activeSession.SessionID, activeSession.Subject, activeSession.ActiveOrganizationID)

	var applications []*model.Application
	var err error
	if activeSession.ActiveOrganizationID != "" {
		applications, err = s.appRepo.FindAllByOrganization(ctx, s.db, activeSession.ActiveOrganizationID)
	} else {
		applications, err = s.appRepo.FindAllByUser(ctx, s.db, activeSession.Subject)
	}
	if err != nil {
		log.Warning(ctx, "applications/List: Failed to list applications: err=%v", err)
		return nil, apierror.Unexpected(err)
	}
	log.Debug(ctx, "applications/List: Found %d applications", len(applications))

	responses := make([]*serialize.ExtendedApplicationResponse, len(applications))
	for i, app := range applications {
		// PERF: we don't use toResponse() because a lot of the queries
		// it executes are irrelevant to this view.
		sm, apiErr := s.convertToSerializableMinimal(ctx, s.db, app)
		if apiErr != nil {
			log.Warning(ctx, "applications/List: convertToSerializableMinimal failed: app=%s err=%v", app.ID, apiErr)
			return nil, apiErr
		}

		serializable := &model.ApplicationSerializable{ApplicationSerializableMinimal: sm}
		currentPlan, err := shpricing.GetCurrentPlan(ctx, s.db, s.subscriptionPlanRepo, sm.Subscription.ID)
		if err != nil {
			log.Warning(ctx, "applications/List: GetCurrentPlan failed: app=%s err=%v", app.ID, err)
			return nil, apierror.Unexpected(err)
		}
		serializable.SubscriptionPlan = currentPlan

		responses[i] = serialize.ExtendedApplication(ctx, serializable)
	}

	log.Debug(ctx, "applications/List: Returning %d applications", len(responses))
	return responses, nil
}

// Read returns the model.Application with the given ID
func (s *Service) Read(ctx context.Context, appID string) (*serialize.ExtendedApplicationResponse, apierror.Error) {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	app, err := s.appRepo.FindByID(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return s.toResponse(ctx, app, activeSession.ActiveOrganizationRole)
}

type MoveToOrganizationParams struct {
	OrganizationID string `json:"organization_id" form:"organization_id" validate:"required"`
}

func (params *MoveToOrganizationParams) validate() apierror.Error {
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

// TransferToOrganization transfers the ownership of the given application to the given organization id.
// The requesting user needs to be a member of that organization.
func (s *Service) TransferToOrganization(ctx context.Context, applicationID string, params MoveToOrganizationParams) apierror.Error {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	apiErr := params.validate()
	if apiErr != nil {
		return apiErr
	}

	belongsToOrg, err := s.applicationOwnershipRepo.ExistsAppOrganizationOwner(ctx, s.db, params.OrganizationID, applicationID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if belongsToOrg {
		return apierror.ApplicationAlreadyBelongsToOrganization()
	}

	activeMembership, err := s.organizationMembershipRepo.QueryByOrganizationAndUser(ctx, s.db, params.OrganizationID, activeSession.Subject)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if activeMembership == nil || !activeMembership.HasRole(constants.RoleAdmin) {
		return apierror.NotAuthorizedToMoveApplicationToOrganization(applicationID, params.OrganizationID)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.applicationOwnershipRepo.DeleteAllAppOwnerships(ctx, tx, applicationID)
		if err != nil {
			return true, err
		}

		newOwnership := &model.ApplicationOwnership{
			ApplicationOwnership: &sqbmodel.ApplicationOwnership{
				ApplicationID:  applicationID,
				OrganizationID: null.StringFrom(params.OrganizationID),
			},
		}

		err = s.applicationOwnershipRepo.Insert(ctx, tx, newOwnership)
		if err != nil {
			return true, err
		}

		err = s.transferSubscription(ctx, tx, applicationID, params.OrganizationID)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}
		return apierror.Unexpected(txErr)
	}
	return nil
}

// TransferToUser moves the given application to the ownership of the requesting
// user.
func (s *Service) TransferToUser(ctx context.Context, applicationID string) apierror.Error {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	belongsToUser, err := s.applicationOwnershipRepo.ExistsAppUserOwner(ctx, s.db, activeSession.Subject, applicationID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if belongsToUser {
		return apierror.ApplicationAlreadyBelongsToUser()
	}

	if activeSession.ActiveOrganizationRole != constants.RoleAdmin {
		return apierror.NotAnAdminInOrganization()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.applicationOwnershipRepo.DeleteAllAppOwnerships(ctx, tx, applicationID)
		if err != nil {
			return true, err
		}

		newOwnership := &model.ApplicationOwnership{
			ApplicationOwnership: &sqbmodel.ApplicationOwnership{
				ApplicationID: applicationID,
				UserID:        null.StringFrom(activeSession.Subject),
			},
		}

		err = s.applicationOwnershipRepo.Insert(ctx, tx, newOwnership)
		if err != nil {
			return true, err
		}

		err = s.transferSubscription(ctx, tx, applicationID, activeSession.Subject)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}
		return apierror.Unexpected(txErr)
	}
	return nil
}

func (s *Service) transferSubscription(ctx context.Context, tx database.Tx, resourceID, newOwnerID string) error {
	subscription, err := s.subscriptionRepo.FindByResourceIDForUpdate(ctx, tx, resourceID)
	if err != nil {
		return err
	}

	if !subscription.StripeSubscriptionID.Valid {
		// this is not a paid app, so no need to update the subscription
		return nil
	}

	billingAccount, err := s.billingAcctRepo.QueryByOwnerID(ctx, tx, newOwnerID)
	if err != nil {
		return err
	}
	if billingAccount == nil || !billingAccount.StripeCustomerID.Valid {
		// New owner doesn't have a Stripe customer associated with it, so we
		// cannot transfer a paid application.
		return apierror.CannotTransferPaidAppToAccountWithoutBillingInformation()
	}

	subscription.BillingAccountID = null.StringFrom(billingAccount.ID)
	err = s.subscriptionRepo.UpdateBillingAccount(ctx, tx, subscription)
	if err != nil {
		return err
	}

	subscriptionPlans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, subscription.ID)
	if err != nil {
		return err
	}
	stripeProductIDs := make([]string, 0, len(subscriptionPlans))
	for _, subscriptionPlan := range subscriptionPlans {
		if !subscriptionPlan.StripeProductID.Valid {
			continue
		}
		stripeProductIDs = append(stripeProductIDs, subscriptionPlan.StripeProductID.String)
	}

	prices, err := s.subscriptionPriceRepo.FindAllByStripeProduct(ctx, tx, stripeProductIDs...)
	if err != nil {
		return err
	}

	fetchPaymentMethodParams := &stripe.PaymentMethodListParams{
		Customer: stripe.String(billingAccount.StripeCustomerID.String),
		Type:     stripe.String(string(stripe.PaymentMethodTypeCard)),
	}
	paymentMethod, err := s.paymentProvider.FetchPaymentMethod(cenv.Get(cenv.StripeSecretKey), fetchPaymentMethodParams)
	if err != nil {
		return err
	}
	if paymentMethod == nil {
		return apierror.CannotTransferToAccountWithoutPaymentMethod()
	}

	oldStripeSubscription, err := s.paymentProvider.FetchSubscription(subscription.StripeSubscriptionID.String)
	if err != nil {
		return err
	}

	// calculate the amount of credit to apply to the new subscription
	var totalCreditAmount int64
	for _, subItem := range oldStripeSubscription.Items.Data {
		subPrice := subItem.Price
		if subPrice == nil {
			continue
		}

		// only fixed price items are eligible for credit
		// if metadata is missing, assume it is a fixed price
		val, ok := subPrice.Metadata["metric"]
		if !ok || val == clerkbilling.PriceTypes.Fixed {
			totalCreditAmount += subPrice.UnitAmount
		}
	}

	// create the new subscription with a credit for the fixed price already paid
	newSubscriptionParams := clerkbilling.CreateSubscriptionParams{
		ResourceID:      resourceID,
		CustomerID:      billingAccount.StripeCustomerID.Ptr(),
		PaymentMethodID: &paymentMethod.ID,
		Prices:          prices,
		CreditAmount:    &totalCreditAmount,
		StartDate:       &oldStripeSubscription.CurrentPeriodStart,
		NextBillingDate: &oldStripeSubscription.CurrentPeriodEnd,
	}

	// add existing trial end period to the new subscription
	if oldStripeSubscription.TrialEnd > 0 && oldStripeSubscription.TrialEnd > s.clock.Now().Unix() {
		newSubscriptionParams.TrialPeriodEnds = &oldStripeSubscription.TrialEnd
	}

	newStripeSubscription, err := s.paymentProvider.CreateSubscription(newSubscriptionParams)
	if err != nil {
		return err
	}

	oldStripeSubscriptionID := subscription.StripeSubscriptionID

	// update our subscription model with the new Stripe subscription, and cancel the old one.
	apiErr := s.sharedPricingService.ApplyStripeSubscription(ctx, tx, subscription, newStripeSubscription)
	if apiErr != nil {
		return apiErr
	}

	// cancel old subscription if we were already on a paid plan, to avoid double-charging
	if oldStripeSubscriptionID.Valid {
		cancelSubscriptionParams := clerkbilling.CancelSubscriptionParams{
			InvoiceNow: false,
			Prorate:    false,
		}
		err = s.paymentProvider.CancelSubscription(ctx, oldStripeSubscriptionID.String, cancelSubscriptionParams)
		if err != nil {
			wrapped := fmt.Errorf("failed to cancel subscription %s: %w", oldStripeSubscriptionID.String, err)
			sentryclerk.CaptureException(ctx, wrapped)
		}
	}
	return nil
}

func (s *Service) toResponse(ctx context.Context, app *model.Application, userRole string) (*serialize.ExtendedApplicationResponse, apierror.Error) {
	applicationSerializable, apiErr := s.convertToSerializable(ctx, s.db, app, userRole)
	if apiErr != nil {
		return nil, apiErr
	}

	return serialize.ExtendedApplication(ctx, applicationSerializable), nil
}

func (s *Service) ListPlans(ctx context.Context, appID string) ([]*serialize.SubscriptionPlanWithPricesResponse, apierror.Error) {
	subscription, err := s.subscriptionRepo.FindByResourceID(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	subscriptionProducts, err := s.subscriptionProductsRepo.FindAllBySubscriptionID(ctx, s.db, subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	subscriptionPlanIDs := make([]string, len(subscriptionProducts))
	for i, subscriptionProduct := range subscriptionProducts {
		subscriptionPlanIDs[i] = subscriptionProduct.SubscriptionPlanID
	}
	availablePlans, err := s.subscriptionPlanRepo.FindAvailableForResource(ctx, s.db, subscriptionPlanIDs, appID, constants.ApplicationResource)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	currentPlans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, s.db, subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	planHierarchy := buildPlanHierarchy(availablePlans)
	sortPlans(availablePlans, planHierarchy)

	addons := make(map[string]*model.SubscriptionPlan)
	for _, plan := range availablePlans {
		if !plan.IsAddon {
			continue
		}
		addons[plan.ID] = plan
	}

	response := make([]*serialize.SubscriptionPlanWithPricesResponse, 0)
	for _, plan := range availablePlans {
		if plan.IsAddon {
			continue
		}

		planWithPrices, err := s.toSubscriptionPlanWithPrices(ctx, plan)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		planAddons := make([]*model.SubscriptionPlanWithPrices, 0)
		for _, addonID := range plan.Addons {
			addon, exists := addons[addonID]
			if !exists {
				continue
			}
			addonWithPrices, err := s.toSubscriptionPlanWithPrices(ctx, addon)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}
			planAddons = append(planAddons, addonWithPrices)
		}

		action := determineAction(currentPlans, plan.ID, planHierarchy)
		response = append(response, serialize.SubscriptionPlanWithPrices(
			planWithPrices,
			serialize.WithAction(action),
			serialize.WithAddons(planAddons),
			serialize.WithSubscriptionPlanEnterpriseIndication(plan.IsVisibleToApplicationID(appID)),
		))
	}

	return response, nil
}

func (s *Service) CurrentSubscription(ctx context.Context, appID string) (*dapiserialize.CurrentApplicationSubscriptionResponse, apierror.Error) {
	currentSubscription, err := s.subscriptionService.CurrentApplicationSubscription(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return currentSubscription, nil
}

func buildPlanHierarchy(plans []*model.SubscriptionPlan) map[string]string {
	planHierarchy := make(map[string]string)
	for _, plan := range plans {
		if !plan.BasePlan.Valid {
			continue
		}
		planHierarchy[plan.ID] = plan.BasePlan.String
	}
	return planHierarchy
}

// determineAction returns the type of action required to go from the active plan to the given one.
// * `upgrade` when going from a lower plan to a higher in the same hierarchy
// * `downgrade` when going from a higher to a lower plan in the same hierarchy
// * `switch` when going to a plan from a separate hierarchy
func determineAction(subscribedPlans []*model.SubscriptionPlan, newPlanID string, planHierarchy map[string]string) string {
	activePlanID := ""
	currentPlan := clerkbilling.DetectCurrentPlan(subscribedPlans)
	if currentPlan != nil {
		activePlanID = currentPlan.ID
	}
	if activePlanID == newPlanID {
		return ""
	}
	if isHigherInHierarchy(newPlanID, activePlanID, planHierarchy) {
		return "upgrade"
	} else if isHigherInHierarchy(activePlanID, newPlanID, planHierarchy) {
		return "downgrade"
	}
	return "switch"
}

// sortPlans sorts the given plans based on their hierarchy or, if in a different hierarchy,
// based on the mau_limit (for backwards compatibility)
func sortPlans(plans []*model.SubscriptionPlan, planHierarchy map[string]string) {
	sort.Slice(plans, func(i, j int) bool {
		first := plans[i]
		second := plans[j]

		if first.BasePlan.Valid && isHigherInHierarchy(first.ID, second.ID, planHierarchy) {
			return false
		} else if second.BasePlan.Valid && isHigherInHierarchy(second.ID, first.ID, planHierarchy) {
			return true
		}
		return first.MonthlyUserLimit < second.MonthlyUserLimit
	})
}

// isHigherInHierarchy checks whether the source plan is in the same hierarchy and it's higher than the target plan.
// For example, if we have the following hierarchy: Free, Hobby, Business,
// then the following calls will return true:
// isHigherInHierarchy("Hobby", "Free")
// isHigherInHierarchy("Business", "Free")
// isHigherInHierarchy("Business", "Hobby")
func isHigherInHierarchy(sourcePlan, targetPlan string, planHierarchy map[string]string) bool {
	for sourcePlan != "" {
		basePlan, ok := planHierarchy[sourcePlan]
		if !ok {
			return false
		}
		if basePlan == targetPlan {
			return true
		}
		sourcePlan = basePlan
	}
	return false
}

func (s *Service) toSubscriptionPlanWithPrices(ctx context.Context, subscriptionPlan *model.SubscriptionPlan) (*model.SubscriptionPlanWithPrices, error) {
	prices, err := s.subscriptionPriceRepo.FindAllByStripeProduct(ctx, s.db, subscriptionPlan.StripeProductID.String)
	if err != nil {
		return nil, err
	}
	return model.NewSubscriptionPlanWithPrices(subscriptionPlan, prices), nil
}

// RefreshPaymentStatus checks the payment status for a particular checkout session
// To be called by the UI after the redirect back to Clerk from the payment provider
func (s *Service) RefreshPaymentStatus(ctx context.Context, appID string, refreshPaymentStatusParams params.RefreshPaymentStatusParams) (*serialize.SubscriptionPlanResponse, apierror.Error) {
	subscription, err := s.subscriptionRepo.FindByResourceID(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	checkoutSessionID, apiErr := determineCheckoutSessionID(ctx, appID, subscription, refreshPaymentStatusParams)
	if apiErr != nil {
		return nil, apiErr
	}

	if checkoutSessionID != "" {
		checkoutSession, err := s.paymentProvider.FetchCheckoutSession(checkoutSessionID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		apiErr = s.pricingService.CheckoutSessionCompleted(ctx, *checkoutSession)
		if apiErr != nil {
			return nil, apiErr
		}
	}

	currentPlan, err := shpricing.GetCurrentPlan(ctx, s.db, s.subscriptionPlanRepo, subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.SubscriptionPlan(currentPlan), nil
}

func (s *Service) CheckAdminIfOrganizationActive(ctx context.Context) apierror.Error {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	if activeSession.ActiveOrganizationID == "" {
		// no active organization, nothing else to check
		return nil
	}

	activeMembership, err := s.organizationMembershipRepo.QueryByOrganizationAndUser(ctx, s.db, activeSession.ActiveOrganizationID, activeSession.Subject)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if activeMembership == nil || !activeMembership.HasRole(constants.RoleAdmin) {
		return apierror.NotAnAdminInOrganization()
	}
	return nil
}

func determineCheckoutSessionID(ctx context.Context, appID string, subscription *model.Subscription, refreshPaymentStatusParams params.RefreshPaymentStatusParams) (string, apierror.Error) {
	// If provided in params, it needs sto match value stored on the app
	if refreshPaymentStatusParams.CheckoutSessionID != "" && refreshPaymentStatusParams.CheckoutSessionID != subscription.StripeCheckoutSessionID.String {
		apiErr := apierror.CheckoutSessionMismatch(appID, refreshPaymentStatusParams.CheckoutSessionID)
		sentryclerk.CaptureException(ctx, apiErr)
		return "", apiErr
	}

	return subscription.StripeCheckoutSessionID.String, nil
}

func (s *Service) convertToSerializableMinimal(ctx context.Context, exec database.Executor, app *model.Application) (*model.ApplicationSerializableMinimal, apierror.Error) {
	serializable := &model.ApplicationSerializableMinimal{Application: app}

	var err error
	serializable.Subscription, err = s.subscriptionRepo.FindByResourceID(ctx, exec, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializable.Instances, err = s.instanceRepo.FindAllByApplication(ctx, exec, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if len(serializable.Instances) > 0 {
		serializable.DisplayConfig, err = s.displayConfigRepo.FindByID(ctx, exec, serializable.Instances[0].ActiveDisplayConfigID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	serializable.IntegrationTypes, err = s.integrationRepo.FindAllTypesByApplication(ctx, exec, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serializable, nil
}

func (s *Service) convertToSerializable(
	ctx context.Context,
	exec database.Executor,
	app *model.Application,
	userRole string) (*model.ApplicationSerializable, apierror.Error) {
	serializableMinimal, apiErr := s.convertToSerializableMinimal(ctx, exec, app)
	if apiErr != nil {
		return nil, apiErr
	}

	serializable := &model.ApplicationSerializable{ApplicationSerializableMinimal: serializableMinimal}

	currentPlan, err := shpricing.GetCurrentPlan(ctx, s.db, s.subscriptionPlanRepo, serializable.Subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	serializable.SubscriptionPlan = currentPlan

	serializable.AppImages, err = s.imagesRepo.AppImages(ctx, exec, app)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializable.UserAccessibleFeatures = authorization.AccessibleFeatures(userRole)

	serializable.HasActiveProductionInstance, err = s.applicationService.HasRecentlyActiveProductionInstance(ctx, s.db, s.clock, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serializable, nil
}

func (s *Service) UnsubscribeFromAddon(ctx context.Context, applicationID, addonPlanID string) apierror.Error {
	application, err := s.appRepo.FindByID(ctx, s.db, applicationID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	var planToRemove *model.SubscriptionPlan
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		subscription, err := s.subscriptionRepo.FindByResourceIDForUpdate(ctx, tx, applicationID)
		if err != nil {
			return true, err
		}

		subscriptionProductForAddon, err := s.subscriptionProductsRepo.QueryBySubscriptionIDProductID(ctx, tx, subscription.ID, addonPlanID)
		if err != nil {
			return true, err
		}
		if subscriptionProductForAddon == nil {
			return true, apierror.ResourceNotFound()
		}

		planToRemove, err = s.subscriptionPlanRepo.QueryByID(ctx, tx, addonPlanID)
		if err != nil {
			return true, err
		}
		if planToRemove == nil {
			return true, apierror.ResourceNotFound()
		}

		// Now fetch the current subscription to go over all the subscribed items and decide which ones
		// need to be retained and which ones to be removed.
		stripeSubscription, err := s.paymentProvider.FetchSubscription(subscription.StripeSubscriptionID.String)
		if err != nil {
			return true, err
		}

		itemsToRemoveFromSubscription := make([]*stripe.SubscriptionItem, 0)
		for _, subscriptionItem := range stripeSubscription.Items.Data {
			if subscriptionItem.Price.Product.ID != planToRemove.StripeProductID.String {
				// This subscription item belongs to another product, so we skip it
				continue
			}
			itemsToRemoveFromSubscription = append(itemsToRemoveFromSubscription, subscriptionItem)
		}

		// The following block is the new logic for the grace period.
		// For the moment, it just populates the new `grace_period_features` column
		// in subscription, but we're still using the old way.
		// Once all records have been migrated to the new one, we'll only rely on that.
		newSetOfPlans, err := s.subscriptionPlanRepo.FindAllBySubscriptionExcludingIDs(ctx, tx, subscription.ID, planToRemove.ID)
		if err != nil {
			return true, err
		}

		billableApplicationResource := s.sharedPricingService.BillableResourceForApplication(ctx, tx, application)
		previousPlans := append(newSetOfPlans, planToRemove)
		err = s.sharedPricingService.HandleGracePeriod(ctx, tx, billableApplicationResource, subscription, previousPlans, newSetOfPlans...)
		if err != nil {
			return true, err
		}

		err = s.subscriptionProductsRepo.DeleteByID(ctx, tx, subscriptionProductForAddon.ID)
		if err != nil {
			return true, err
		}

		if cenv.IsEnabled(cenv.FlagAutoRefundCanceledSubscriptions) {
			refundItems, err := s.paymentProvider.DetermineRefundItems(stripeSubscription.ID, itemsToRemoveFromSubscription)
			if err != nil {
				return true, err
			}

			err = jobs.Refund(ctx, s.gueClient, jobs.RefundArgs{
				CustomerID:     stripeSubscription.Customer.ID,
				SubscriptionID: stripeSubscription.ID,
				RefundItems:    refundItems,
			}, jobs.WithTx(tx))
			if err != nil {
				return true, err
			}
		}

		updatedSubscription, err := s.paymentProvider.RemoveFromSubscription(ctx, clerkbilling.RemoveFromSubscriptionParams{
			SubscriptionID:           stripeSubscription.ID,
			DeletedSubscriptionItems: itemsToRemoveFromSubscription,
		})
		if err != nil {
			return true, err
		}

		err = s.sharedPricingService.ApplyStripeSubscription(ctx, tx, subscription, updatedSubscription)
		if err != nil {
			// in case of failure, it's safe to ignore the error...for staging and production, we will also
			// have an incoming webhook event, so we're good
			log.Warning(ctx, "updated Stripe subscription successfully, but failed to apply changes locally: %s",
				err.Error())
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}
		return apierror.Unexpected(txErr)
	}

	s.notifyProductChange(ctx, application, planToRemove, segment.APIBackendBillingPlanAddOnDisabled, addOnActionDisabled)
	return nil
}

type subscribeToProductParams struct {
	applicationID string
	productID     string
}

// SubscribeToProduct enables a product for the application's current subscription.
// The subscription is first updated on the payment provider's side with the new
// product prices. Then, we do the necessary book-keeping on Clerk's side to make
// sure we add subscription products and metrics records.
// The new product must be supported by the subscription's current plan.
func (s *Service) SubscribeToProduct(ctx context.Context, params subscribeToProductParams) (any, apierror.Error) {
	product, err := s.subscriptionPlanRepo.QueryByID(ctx, s.db, params.productID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if product == nil {
		return nil, apierror.ResourceNotFound()
	}

	clerkSubscription, err := s.subscriptionRepo.FindByResourceID(ctx, s.db, params.applicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	currentPlan, err := shpricing.GetCurrentPlan(ctx, s.db, s.subscriptionPlanRepo, clerkSubscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if !set.New[string](currentPlan.Addons...).Contains(product.ID) {
		return nil, apierror.ProductNotSupportedBySubscriptionPlan(product.ID)
	}

	currentMetrics, err := s.subscriptionMetricsRepo.FindAllBySubscriptionID(ctx, s.db, clerkSubscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	application, err := s.appRepo.FindByID(ctx, s.db, params.applicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	stripeSubscription, err := s.subscribeToProductOnProvider(ctx, clerkSubscription, product, currentMetrics)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	apiErr := s.subscribeToProductOnClerk(ctx, application, clerkSubscription, stripeSubscription, product, currentMetrics)
	if apiErr != nil {
		return nil, apiErr
	}

	s.notifyProductChange(ctx, application, product, segment.APIBackendBillingPlanAddOnEnabled, addOnActionEnabled)

	return serialize.Subscription(clerkSubscription, currentPlan), nil
}

func (s *Service) subscribeToProductOnProvider(
	ctx context.Context,
	clerkSubscription *model.Subscription,
	product *model.SubscriptionPlan,
	existingMetrics []*model.SubscriptionMetric,
) (*stripe.Subscription, error) {
	if !clerkSubscription.StripeSubscriptionID.Valid {
		return nil, nil
	}

	productPrices, err := s.subscriptionPriceRepo.FindAllByStripeProduct(ctx, s.db, product.StripeProductID.String)
	if err != nil {
		return nil, err
	}
	updateParams := clerkbilling.AddToSubscriptionParams{
		SubscriptionID: clerkSubscription.StripeSubscriptionID.String,
	}

	metricToSubscriptionMetric := make(map[string]*model.SubscriptionMetric)
	for _, subscriptionMetric := range existingMetrics {
		metricToSubscriptionMetric[subscriptionMetric.Metric] = subscriptionMetric
	}

	for _, price := range productPrices {
		if !price.Active {
			continue
		}
		if _, exists := metricToSubscriptionMetric[price.Metric]; exists {
			// We are already subscribed to given price
			continue
		}
		updateParams.NewPrices = append(updateParams.NewPrices, &model.SubscriptionPrice{
			SubscriptionPrice: &sqbmodel.SubscriptionPrice{
				StripePriceID: price.StripePriceID,
				Metric:        price.Metric,
				Metered:       price.Metered,
			},
		})
	}

	stripeSubscription, err := s.paymentProvider.AddToSubscription(ctx, updateParams)
	if err != nil {
		return nil, err
	}

	return stripeSubscription, nil
}

// Internal book-keeping to enable a product for a subscription.
// Creates the necessary records and associations and syncs the
// Clerk subscription with the state on the provider (Stripe) side.
func (s *Service) subscribeToProductOnClerk(
	ctx context.Context,
	application *model.Application,
	clerkSubscription *model.Subscription,
	stripeSubscription *stripe.Subscription,
	product *model.SubscriptionPlan,
	existingMetrics []*model.SubscriptionMetric,
) apierror.Error {
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// Enable product for subscription
		err := s.subscriptionProductsRepo.Insert(ctx, tx, model.NewSubscriptionProduct(clerkSubscription.ID, product.ID))
		if err != nil {
			return true, err
		}

		// Update the new grace period features column
		allPlansIncludingNew, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, clerkSubscription.ID)
		if err != nil {
			return true, err
		}

		previousPlans := make([]*model.SubscriptionPlan, 0)
		for _, plan := range allPlansIncludingNew {
			if plan.ID != product.ID {
				previousPlans = append(previousPlans, plan)
			}
		}

		billableApplicationResource := s.sharedPricingService.BillableResourceForApplication(ctx, tx, application)
		err = s.sharedPricingService.HandleGracePeriod(ctx, tx, billableApplicationResource, clerkSubscription, previousPlans, allPlansIncludingNew...)
		if err != nil {
			return true, err
		}

		// Create the necessary subscription metrics
		if stripeSubscription != nil {
			err := s.sharedPricingService.CreateMissingSubscriptionMetrics(
				ctx,
				tx,
				stripeSubscription.Items.Data,
				clerkSubscription.ID,
				existingMetrics,
			)
			if err != nil {
				return true, err
			}
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return apiErr
		}
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueSubscriptionProduct) {
			return apierror.ProductAlreadySubscribed(product.ID)
		}
		return apierror.Unexpected(txErr)
	}
	return nil
}

func (s *Service) notifyProductChange(
	ctx context.Context,
	application *model.Application,
	product *model.SubscriptionPlan,
	eventName segment.EventName,
	action string,
) {
	err := jobs.SegmentEnqueueEvent(
		ctx,
		s.gueClient,
		jobs.SegmentArgs{
			Event:         eventName,
			ApplicationID: application.ID,
			Properties: map[string]any{
				"surface":       "API",
				"location":      "Dashboard",
				"applicationId": application.ID,
				"productTitle":  product.Title,
				"productId":     product.ID,
			},
		},
	)
	if err != nil {
		sentryclerk.CaptureException(ctx, fmt.Errorf("notifyProductChange: cannot enqueue tracking job: %w", err))
	}

	emoji := "🎉"
	if action == addOnActionDisabled {
		emoji = "😔"
	}

	err = jobs.SendSlackAlert(
		ctx,
		s.gueClient,
		jobs.SlackAlertArgs{
			Webhook: constants.SlackBilling,
			Message: slack.Message{
				Title: fmt.Sprintf("%s Add-on %s", emoji, action),
				Text: fmt.Sprintf(
					"Application '%s' (%s) %s add-on '%s' (%s)",
					application.Name,
					application.ID,
					action,
					product.Title,
					product.ID,
				),
				Type: slack.Info,
			},
		},
	)
	if err != nil {
		sentryclerk.CaptureException(ctx, fmt.Errorf("notifyProductChange: cannot send slack alert: %w", err))
	}
}

// OwnershipService handles logic around who owns an application.
type OwnershipService struct {
	db                       database.Database
	applicationOwnershipRepo *repository.ApplicationOwnerships
}

func NewOwnershipService(db database.Database) *OwnershipService {
	return &OwnershipService{
		db:                       db,
		applicationOwnershipRepo: repository.NewApplicationOwnerships(),
	}
}

// AuthorizeUser returns an error if the current session user does
// not own application with appID.
// User are owners of personal workspace applications or applications in
// organizations that they are members of.
func (s *OwnershipService) AuthorizeUser(ctx context.Context, appID string) apierror.Error {
	activeSession, _ := sdkutils.GetActiveSession(ctx)

	var owned bool
	var err error
	if activeSession.ActiveOrganizationID != "" {
		owned, err = s.applicationOwnershipRepo.ExistsAppOrganizationOwner(ctx, s.db, activeSession.ActiveOrganizationID, appID)
	} else {
		owned, err = s.applicationOwnershipRepo.ExistsAppUserOwner(ctx, s.db, activeSession.Subject, appID)
	}
	if err != nil {
		return apierror.Unexpected(err)
	}
	if !owned {
		return apierror.ApplicationNotFound(appID)
	}
	return nil
}

func (s *Service) fetchApplication(ctx context.Context, exec database.Executor, applicationID string) (*model.Application, apierror.Error) {
	application, err := s.appRepo.QueryByID(ctx, exec, applicationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if application == nil {
		return nil, apierror.ApplicationNotFound(applicationID)
	}
	return application, nil
}

func (s *Service) EnsureApplicationNotPendingDeletion(ctx context.Context, applicationID string) apierror.Error {
	application, err := s.appRepo.QueryByID(ctx, s.db, applicationID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if application == nil || application.HardDeleteAt.Valid {
		return apierror.ResourceNotFound()
	}
	return nil
}
