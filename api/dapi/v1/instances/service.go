package instances

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"clerk/api/apierror"
	"clerk/api/dapi/serialize"
	"clerk/api/dapi/v1/applications"
	"clerk/api/dapi/v1/domains"
	sharedserialize "clerk/api/serialize"
	shapplications "clerk/api/shared/applications"
	shdomains "clerk/api/shared/domains"
	"clerk/api/shared/edgereplication"
	shenvironment "clerk/api/shared/environment"
	"clerk/api/shared/features"
	"clerk/api/shared/instances"
	"clerk/model"
	"clerk/model/sqbmodel_extensions"
	"clerk/pkg/apiversioning"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/externalapis/segment"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/generate"
	"clerk/pkg/jobs"
	"clerk/pkg/keygen"
	"clerk/pkg/params"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/segment/dapi"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/set"
	"clerk/pkg/validators"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"
	"clerk/utils/param"
	"clerk/utils/url"

	"github.com/clerk/clerk-sdk-go/v2/instancesettings"
	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock                clockwork.Clock
	db                   database.Database
	gueClient            *gue.Client
	sdkConfigConstructor sdkutils.ConfigConstructor

	// clients
	svixClient        *svix.Client
	clerkImagesClient *clerkimages.Client

	// services
	applicationService     *shapplications.Service
	envService             *shenvironment.Service
	featureService         *features.Service
	domainService          *domains.Service
	sharedDomainService    *shdomains.Service
	instanceService        *instances.Service
	edgeReplicationService *edgereplication.Service

	// repositories
	appRepo                *repository.Applications
	authConfigRepo         *repository.AuthConfig
	dnsChecksRepo          *repository.DNSChecks
	domainRepo             *repository.Domain
	enabledSSOProviderRepo *repository.EnabledSSOProviders
	imageRepo              *repository.Images
	instanceRepo           *repository.Instances
	instanceKeysRepo       *repository.InstanceKeys
	smsCountryTierRepo     *repository.SMSCountryTiers
	subscriptionRepo       *repository.Subscriptions
	subscriptionPlansRepo  *repository.SubscriptionPlans
	userRepo               *repository.Users
	displayConfigRepo      *repository.DisplayConfig
}

func NewService(deps clerk.Deps, svixClient *svix.Client, clerkImagesClient *clerkimages.Client, sdkConfigConstructor sdkutils.ConfigConstructor) *Service {
	return &Service{
		clock:                  deps.Clock(),
		db:                     deps.DB(),
		gueClient:              deps.GueClient(),
		sdkConfigConstructor:   sdkConfigConstructor,
		svixClient:             svixClient,
		clerkImagesClient:      clerkImagesClient,
		applicationService:     shapplications.NewService(),
		envService:             shenvironment.NewService(),
		featureService:         features.NewService(deps.DB(), deps.GueClient()),
		domainService:          domains.NewService(deps, sdkConfigConstructor),
		sharedDomainService:    shdomains.NewService(deps),
		instanceService:        instances.NewService(deps.DB(), deps.GueClient()),
		edgeReplicationService: edgereplication.NewService(deps.GueClient(), cenv.GetBool(cenv.FlagReplicateInstanceToEdgeJobsEnabled)),
		appRepo:                repository.NewApplications(),
		authConfigRepo:         repository.NewAuthConfig(),
		dnsChecksRepo:          repository.NewDNSChecks(),
		domainRepo:             repository.NewDomain(),
		enabledSSOProviderRepo: repository.NewEnabledSSOProviders(),
		imageRepo:              repository.NewImages(),
		instanceRepo:           repository.NewInstances(),
		instanceKeysRepo:       repository.NewInstanceKeys(),
		smsCountryTierRepo:     repository.NewSMSCountryTiers(),
		subscriptionRepo:       repository.NewSubscriptions(),
		subscriptionPlansRepo:  repository.NewSubscriptionPlans(),
		userRepo:               repository.NewUsers(),
		displayConfigRepo:      repository.NewDisplayConfig(),
	}
}

// CreateProduction creates a production instance for the given application
func (s *Service) CreateProduction(
	ctx context.Context,
	appID string,
	productionInstanceSettings *params.ProductionInstanceSettings,
) (*serialize.InstanceResponse, apierror.Error) {
	app, err := s.appRepo.FindByID(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	instances, err := s.instanceRepo.FindAllByApplication(ctx, s.db, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	for _, ins := range instances {
		if ins.IsProduction() {
			return nil, apierror.ProductionInstanceExists()
		}
	}

	urlInfo, err := url.Analyze(productionInstanceSettings.HomeURL)
	if err != nil {
		return nil, apierror.FormInvalidParameterValue(param.HomeURL.Name, productionInstanceSettings.HomeURL)
	}
	apiErr := validators.DomainName(validators.DomainNameInput{
		URLInfo:       urlInfo,
		IsDevelopment: false,
		IsSatellite:   false,
		ProxyURL:      nil,
		ParamName:     param.HomeURL.Name,
	})
	if apiErr != nil {
		return nil, apiErr
	}

	// load the instance to clone
	// if we are here, these are non-production instances
	var cloneInstance *model.Instance
	if productionInstanceSettings.CloneInstanceID.IsSet && productionInstanceSettings.CloneInstanceID.Valid {
		for _, ins := range instances {
			if ins.ID == productionInstanceSettings.CloneInstanceID.Value {
				cloneInstance = ins
				break
			}
		}
	}

	estimatedProdInstanceCreatedAt := s.clock.Now().UTC()
	if cloneInstance != nil {
		apiErr := s.checkIfClonedInstanceFeaturesAreSupported(ctx, cloneInstance.ID, estimatedProdInstanceCreatedAt)
		if apiErr != nil {
			return nil, apiErr
		}
	}

	var newInstance *model.Instance
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		newInstance, _, err = generate.Instance(
			ctx,
			txEmitter,
			s.gueClient,
			app,
			urlInfo.Domain,
			null.StringFrom(urlInfo.Origin),
			cloneInstance,
			constants.ETProduction,
			null.StringFromPtr(nil),
			null.JSONFromPtr(nil),
			defaultKeyAlgorithm(),
		)
		if errors.Is(err, generate.ErrDomainTaken) {
			return true, apierror.HomeURLTaken(urlInfo.Domain, param.HomeURL.Name)
		} else if err != nil {
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

	err = s.createAvatarSettings(ctx, newInstance)
	if err != nil {
		// Not fatal if we fail to update Clerk image service, so we only log and continue
		log.Error(ctx, err)
	}

	dapi.EnqueueSegmentEvent(ctx, s.gueClient, dapi.SegmentParams{EventName: segment.APIDashboardProductionInstanceCreated, ApplicationID: appID, InstanceID: newInstance.ID})

	// Start monitoring MAU usage for production instances on the free plan
	subscription, err := s.subscriptionRepo.QueryByResourceID(ctx, s.db, app.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if subscription != nil && !subscription.StripeSubscriptionID.Valid && newInstance.IsProduction() {
		args := jobs.CheckFreeMAULimitArgs{
			InstanceID: newInstance.ID,
		}
		if err := jobs.CheckFreeMAULimit(ctx, s.gueClient, args); err != nil {
			sentryclerk.CaptureException(ctx, fmt.Errorf("cannot schedule free mau usage job for instance %s: %w", newInstance.ID, err))
		}
	}

	return s.fetchModelsAndBuildResponse(ctx, newInstance.ID)
}

func (s *Service) createAvatarSettings(ctx context.Context, instance *model.Instance) error {
	displayConfig, err := s.displayConfigRepo.FindByID(ctx, s.db, instance.ActiveDisplayConfigID)
	if err != nil {
		return err
	}

	err = s.clerkImagesClient.CreateAvatarSettings(ctx, instance.ID, clerkimages.CreateAvatarSettingsParams{
		User:         clerkimages.AvatarSettings(displayConfig.ImageSettings.User),
		Organization: clerkimages.AvatarSettings(displayConfig.ImageSettings.Organization),
	})

	return err
}

type ValidateCloningParams struct {
	ApplicationID   string
	CloneInstanceID string `json:"clone_instance_id" validate:"required"`
}

func (params ValidateCloningParams) Validate() apierror.Error {
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

func (s *Service) ValidateCloning(ctx context.Context, params ValidateCloningParams) apierror.Error {
	apiErr := params.Validate()
	if apiErr != nil {
		return apiErr
	}

	instanceToClone, err := s.instanceRepo.QueryByID(ctx, s.db, params.CloneInstanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if instanceToClone == nil || instanceToClone.ApplicationID != params.ApplicationID {
		return apierror.InstanceNotFound(params.CloneInstanceID)
	}

	estimatedProdInstanceCreatedAt := s.clock.Now().UTC()
	return s.checkIfClonedInstanceFeaturesAreSupported(ctx, instanceToClone.ID, estimatedProdInstanceCreatedAt)
}

func (s *Service) fetchModelsAndBuildResponse(ctx context.Context, instanceID string) (*serialize.InstanceResponse, apierror.Error) {
	env, err := s.envService.Load(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	images, err := s.imageRepo.AppImages(ctx, s.db, env.Application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	plans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	planFeatures := set.New[string]()
	for _, plan := range plans {
		planFeatures.Insert(plan.Features...)
	}

	// XXX Account for features that were on the free plan before our
	// Pricing V2 release.
	// TODO(gkats 2023-12-06) The supported_features and premium_features
	// keys need to be deprecated in favor of the features key.
	if cenv.IsBeforeCutoff(cenv.PricingV2PaidFeaturesCutoffEpochTime, env.Instance.CreatedAt) {
		planFeatures.Insert(billing.FeaturesThatMovedToPaidAfterPricingV2()...)
	}

	premiumFeatures := set.New(billing.AllFeatures()...)
	premiumFeatures.Subtract(planFeatures)

	var supportedFeatures []string
	if env.Instance.HasAccessToAllFeatures() {
		supportedFeatures = billing.AllFeatures()
	} else {
		supportedFeatures = planFeatures.Array()
	}

	availablePlans, err := s.applicationService.GetAvailableSubscriptionPlans(ctx, s.db, env.Application.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Instance(
		ctx,
		env,
		images,
		premiumFeatures.Array(),
		supportedFeatures,
		plans,
		availablePlans,
	), nil
}

// List returns all instances for the given application
func (s *Service) List(ctx context.Context, appID string) (serialize.InstancesResponse, apierror.Error) {
	instances, err := s.instanceRepo.FindAllByApplication(ctx, s.db, appID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	instanceIDs := make([]string, len(instances))
	userExistsByInstance, err := s.userRepo.ExistsForInstances(ctx, s.db, instanceIDs)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]*serialize.InstanceResponse, len(instances))
	for idx, ins := range instances {
		response, err := s.fetchModelsAndBuildResponse(ctx, ins.ID)
		if err != nil {
			return nil, err
		}
		response.HasUsers = userExistsByInstance[ins.ID]
		responses[idx] = response
	}

	return responses, nil
}

// Read returns the requested instance
func (s *Service) Read(ctx context.Context, instanceID string) (*serialize.InstanceResponse, apierror.Error) {
	userExistsByInstance, err := s.userRepo.ExistsForInstances(ctx, s.db, []string{instanceID})
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response, err := s.fetchModelsAndBuildResponse(ctx, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	response.HasUsers = userExistsByInstance[instanceID]
	return response, nil
}

// Delete deletes the given instance
func (s *Service) Delete(ctx context.Context, instanceID string) apierror.Error {
	instance, err := s.instanceRepo.FindByID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if cenv.IsEnabled(cenv.FlagPreventDeletionOfActiveProdInstance) && instance.IsProduction() {
		hasActiveProductionInstance, err := s.applicationService.HasRecentlyActiveProductionInstance(ctx, s.db, s.clock, instance.ApplicationID)
		if err != nil {
			return apierror.Unexpected(err)
		}
		if hasActiveProductionInstance {
			return apierror.CannotDeleteActiveProductionInstance()
		}
	}

	domains, err := s.domainRepo.FindAllByInstanceID(ctx, s.db, instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		// Cleanup resources for all the instance's domains
		for _, domain := range domains {
			err := s.sharedDomainService.Delete(ctx, txEmitter, domain)
			if err != nil {
				return true, err
			}
		}

		err = s.instanceRepo.DeleteByID(ctx, txEmitter, instance.ID)
		return err != nil, err
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	if !instance.IsSvixEnabled() {
		return nil
	}

	err = s.svixClient.Delete(instance.SvixAppID.String)
	if err != nil {
		// do not fail, just report the error to sentry
		sentryclerk.CaptureException(ctx, fmt.Errorf("instance: failed to delete Svix app %s for instance %s: %w",
			instance.SvixAppID.String, instance.ID, err))
	}

	return nil
}

// Update updates certain settings on the given instance
func (s *Service) Update(ctx context.Context, instanceID string, params instancesettings.UpdateParams) apierror.Error {
	sdkConfig, apiErr := sdkutils.NewConfigForInstance(ctx, s.sdkConfigConstructor, s.db, instanceID)
	if apiErr != nil {
		return apiErr
	}

	sdkErr := instancesettings.NewClient(sdkConfig).Update(ctx, &params)
	if sdkErr != nil {
		return sdkutils.ToAPIError(sdkErr)
	}
	return nil
}

type updateAPIVersionParams struct {
	Version string `json:"version" validate:"required"`
}

func (params updateAPIVersionParams) Validate() apierror.Error {
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

func (s *Service) UpdateCommunication(ctx context.Context, params updateCommunicationParams) apierror.Error {
	env := environment.FromContext(ctx)

	if params.BlockedCountryCodes != nil {
		// TODO(mark) Find a way to load these once
		countryCodes, err := s.smsCountryTierRepo.CountryCodes(ctx, s.db)
		if err != nil {
			return apierror.Unexpected(err)
		}

		countryCodeSet := set.New(countryCodes...)
		blockedCountryCodeSet := set.New(*params.BlockedCountryCodes...)

		blockedCountryCodes := set.Intersection(blockedCountryCodeSet, countryCodeSet).Array()
		slices.Sort(blockedCountryCodes)

		env.Instance.Communication.BlockedCountryCodes = blockedCountryCodes

		err = s.instanceRepo.UpdateCommunication(ctx, s.db, env.Instance)
		if err != nil {
			return apierror.Unexpected(err)
		}
	}

	return nil
}

func (s *Service) UpdateAPIVersion(ctx context.Context, instanceID string, params updateAPIVersionParams) apierror.Error {
	apiErr := params.Validate()
	if apiErr != nil {
		return apiErr
	}

	version, found := apiversioning.GetVersion(params.Version)
	if !found {
		return apierror.InvalidAPIVersion(fmt.Sprintf("version '%s' is not supported", params.Version))
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		instance, err := s.instanceRepo.QueryByIDForUpdate(ctx, s.db, instanceID)
		if err != nil {
			return true, apierror.Unexpected(err)
		}
		if instance == nil {
			return true, apierror.InstanceNotFound(instanceID)
		}

		instance.APIVersion = version.GetName()
		err = s.instanceRepo.UpdateAPIVersion(ctx, s.db, instance)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return apiErr
		}
		return apierror.Unexpected(txErr)
	}
	return nil
}

func (s *Service) GetAvailableAPIVersions() []*sharedserialize.APIVersionResponse {
	versions := apiversioning.GetStableVersions()
	responses := make([]*sharedserialize.APIVersionResponse, len(versions))
	for idx, version := range versions {
		responses[idx] = sharedserialize.APIVersion(version)
	}
	return responses
}

func (s *Service) checkIfClonedInstanceFeaturesAreSupported(ctx context.Context, instanceToClone string, newInstanceCreatedAt time.Time) apierror.Error {
	// verify that features of the cloned instance are according to subscription
	// if not, don't allow the creation of a production instance
	env, err := s.envService.Load(ctx, s.db, instanceToClone)
	if err != nil {
		return apierror.Unexpected(err)
	}
	plans, err := s.subscriptionPlansRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	unsupportedFeatures, err := s.featureService.UnsupportedFeatures(ctx, s.db, env, newInstanceCreatedAt, plans...)
	if err != nil {
		return apierror.Unexpected(err)
	}

	unsupportedFeaturesSet := set.New(unsupportedFeatures...)
	// During cloning from dev to prod, ignore saml settings as we don't copy the Enterprise connections over
	unsupportedFeaturesSet.Remove(billing.Features.SAML)

	if !unsupportedFeaturesSet.IsEmpty() {
		return apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeaturesSet.Array())
	}
	return nil
}

type OwnershipService struct {
	db                      database.Database
	applicationOwnershipSvc *applications.OwnershipService
	instanceRepo            *repository.Instances
}

func NewOwnershipService(db database.Database) *OwnershipService {
	return &OwnershipService{
		db:                      db,
		applicationOwnershipSvc: applications.NewOwnershipService(db),
		instanceRepo:            repository.NewInstances(),
	}
}

// CheckInstanceOwner checks whether the active session's user is the owner of the instance's application
func (s *OwnershipService) CheckInstanceOwner(ctx context.Context, instanceID string) apierror.Error {
	instance, err := s.instanceRepo.QueryByID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	} else if instance == nil {
		return apierror.InstanceNotFound(instanceID)
	}

	return s.applicationOwnershipSvc.AuthorizeUser(ctx, instance.ApplicationID)
}

func defaultKeyAlgorithm() keygen.Algorithm {
	if cenv.IsEnabled(cenv.FlagEddsaOnNewInstances) {
		return keygen.EdDSA{}
	}

	return keygen.RSA{}
}

type updatePatchMePasswordParams struct {
	State string `json:"state"`
}

func (s *Service) UpdatePatchMePassword(ctx context.Context, instanceID string, params updatePatchMePasswordParams) apierror.Error {
	// validate that state has a valid value
	states := sqbmodel_extensions.AuthConfigExperimentalSettingsPatchMePasswordStates
	if !states.Contains(params.State) {
		return apierror.InvalidRequestBody(fmt.Errorf("state '%s' is not supported", params.State))
	}

	instance, err := s.instanceRepo.FindByID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if !instance.IsProduction() {
		return apierror.InstanceTypeInvalid()
	}

	authConfig, err := s.authConfigRepo.FindByID(ctx, s.db, instance.ActiveAuthConfigID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		authConfig.ExperimentalSettings.PatchMePasswordState = params.State
		err := s.authConfigRepo.UpdateExperimentalSettings(ctx, txEmitter, authConfig)
		return err != nil, err
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	return nil
}

// DeployStatus checks the deployment status of all the domains for the
// instance specified by instanceID.
// If at least one of the domains is not deployed, the overall status is
// incomplete.
func (s *Service) DeployStatus(ctx context.Context, instanceID string) (*serialize.InstanceDeployStatusResponse, apierror.Error) {
	instance, err := s.instanceRepo.QueryByID(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if instance == nil {
		return nil, apierror.ResourceNotFound()
	}

	status := constants.InstanceDeployStatusIncomplete
	isDeployed, err := s.instanceService.IsDeployed(ctx, instance)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if isDeployed {
		status = constants.InstanceDeployStatusComplete
	}

	return serialize.InstanceDeployStatus(status), nil
}

func (s *Service) RetrySSL(ctx context.Context, instanceID string) apierror.Error {
	instance, err := s.instanceRepo.FindByID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	return s.domainService.RetrySSL(ctx, instance.ID, instance.ActiveDomainID)
}

func (s *Service) RetryMail(ctx context.Context, instanceID string) apierror.Error {
	instance, err := s.instanceRepo.FindByID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	return s.domainService.RetryMail(ctx, instance.ID, instance.ActiveDomainID)
}

func (s *Service) EnsureApplicationNotPendingDeletion(ctx context.Context, instanceID string) apierror.Error {
	application, err := s.appRepo.QueryByInstanceID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if application == nil || application.HardDeleteAt.Valid {
		return apierror.ResourceNotFound()
	}
	return nil
}
