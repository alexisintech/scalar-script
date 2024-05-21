package instances

import (
	"context"
	"math"
	netURL "net/url"
	"regexp"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/domains"
	"clerk/api/shared/edgereplication"
	"clerk/api/shared/organizations"
	"clerk/api/shared/validators"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/generate"
	"clerk/pkg/oauth"
	"clerk/pkg/oauth/provider"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	clerkValidators "clerk/pkg/validators"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/url"
	"clerk/utils/validate"

	"github.com/go-playground/validator/v10"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	db        database.Database
	gueClient *gue.Client

	// repositories
	authConfigRepo       *repository.AuthConfig
	displayConfigRepo    *repository.DisplayConfig
	domainRepo           *repository.Domain
	instanceRepo         *repository.Instances
	permissionRepo       *repository.Permission
	roleRepo             *repository.Role
	subscriptionPlanRepo *repository.SubscriptionPlans
	validator            *validator.Validate

	domainService          *domains.Service
	organizationsService   *organizations.Service
	edgeReplicationService *edgereplication.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                   deps.DB(),
		gueClient:            deps.GueClient(),
		authConfigRepo:       repository.NewAuthConfig(),
		displayConfigRepo:    repository.NewDisplayConfig(),
		domainRepo:           repository.NewDomain(),
		instanceRepo:         repository.NewInstances(),
		permissionRepo:       repository.NewPermission(),
		roleRepo:             repository.NewRole(),
		subscriptionPlanRepo: repository.NewSubscriptionPlans(),
		validator:            validator.New(),

		domainService:          domains.NewService(deps),
		organizationsService:   organizations.NewService(deps),
		edgeReplicationService: edgereplication.NewService(deps.GueClient(), cenv.GetBool(cenv.FlagReplicateInstanceToEdgeJobsEnabled)),
	}
}

type UpdateInstanceParams struct {
	TestMode                    *bool     `json:"test_mode" form:"test_mode"`
	HIBP                        *bool     `json:"hibp" form:"hibp"`
	EnhancedEmailDeliverability *bool     `json:"enhanced_email_deliverability" form:"enhanced_email_deliverability"`
	SupportEmail                *string   `json:"support_email" form:"support_email"`
	ClerkJSVersion              *string   `json:"clerk_js_version" form:"clerk_js_version"`
	AllowedOrigins              *[]string `json:"allowed_origins" form:"allowed_origins"`
	DevelopmentOrigin           *string   `json:"development_origin" form:"development_origin"`

	// Deprecated: Please use URLBasedSessionSyncing instead
	CookielessDev *bool `json:"cookieless_dev" form:"cookieless_dev"`

	URLBasedSessionSyncing *bool `json:"url_based_session_syncing" form:"url_based_session_syncing"`
}

func validateURL(URL string, paramName string) apierror.Error {
	if URL == "" {
		return nil
	}
	validate := regexp.MustCompile(`^(https?://)`)

	if !validate.MatchString(URL) {
		return apierror.FormInvalidParameterFormat(paramName, "Should start with https:// or http://")
	}

	parsedURL, err := netURL.ParseRequestURI(URL)
	if err != nil || parsedURL == nil || parsedURL.Host == "" {
		return apierror.FormInvalidParameterFormat(paramName, "Must be a valid url")
	}

	return nil
}

func (s *Service) Update(ctx context.Context, params UpdateInstanceParams) apierror.Error {
	env := environment.FromContext(ctx)

	// Enhanced email deliverability is not available for magic links.
	if params.EnhancedEmailDeliverability != nil {
		valErr := validators.ValidateEnhancedEmailDeliverability(
			*params.EnhancedEmailDeliverability,
			usersettings.NewUserSettings(env.AuthConfig.UserSettings),
		)
		if valErr != nil {
			return valErr
		}
	}

	instanceColumns := set.New[string]()
	authConfigColumns := set.New[string]()
	displayConfigColumns := make([]string, 0)
	domainColumns := make([]string, 0)

	if params.TestMode != nil {
		env.AuthConfig.TestMode = *params.TestMode
		authConfigColumns.Insert(sqbmodel.AuthConfigColumns.TestMode)
	}

	if params.HIBP != nil {
		env.AuthConfig.UserSettings.PasswordSettings.DisableHIBP = !*params.HIBP
		authConfigColumns.Insert(sqbmodel.AuthConfigColumns.UserSettings)
	}

	if params.EnhancedEmailDeliverability != nil {
		env.Instance.Communication.EnhancedEmailDeliverability = *params.EnhancedEmailDeliverability
		instanceColumns.Insert(sqbmodel.InstanceColumns.Communication)
	}

	if params.SupportEmail != nil {
		if *params.SupportEmail == "" {
			env.Instance.Communication.SupportEmail = null.StringFromPtr(nil)
		} else {
			err := validate.EmailAddress(*params.SupportEmail, "support_email")
			if err != nil {
				return err
			}
			env.Instance.Communication.SupportEmail = null.StringFrom(*params.SupportEmail)
		}
		instanceColumns.Insert(sqbmodel.InstanceColumns.Communication)
	}

	// No validation of Clerk JS version string for now
	if params.ClerkJSVersion != nil {
		if *params.ClerkJSVersion == "" {
			env.DisplayConfig.ClerkJSVersion = null.StringFromPtr(nil)
		} else {
			env.DisplayConfig.ClerkJSVersion = null.StringFrom(*params.ClerkJSVersion)
		}
		displayConfigColumns = append(displayConfigColumns, sqbmodel.DisplayConfigColumns.ClerkJSVersion)
	}

	rawAllowedOrigins := make([]string, 0)
	if params.AllowedOrigins != nil {
		rawAllowedOrigins = append(rawAllowedOrigins, *params.AllowedOrigins...)
	}

	rawUniqueAllowedOrigins := set.New(rawAllowedOrigins...).Array()

	if len(rawUniqueAllowedOrigins) > 0 {
		var parsedAllowedOrigins []string

		for _, origin := range rawUniqueAllowedOrigins {
			if err := s.validator.Var(origin, "url"); err != nil {
				return apierror.FormInvalidOrigin("allowed_origins")
			}

			parsedOrigin, err := url.ExtractOrigin(origin)
			if err != nil {
				return apierror.FormInvalidOrigin("allowed_origins")
			}
			parsedAllowedOrigins = append(parsedAllowedOrigins, parsedOrigin)
		}

		env.Instance.AllowedOrigins = parsedAllowedOrigins
		instanceColumns.Insert(sqbmodel.InstanceColumns.AllowedOrigins)
	}

	if params.URLBasedSessionSyncing != nil {
		env.AuthConfig.SessionSettings.URLBasedSessionSyncing = *params.URLBasedSessionSyncing
		authConfigColumns.Insert(sqbmodel.AuthConfigColumns.SessionSettings)
	} else if params.CookielessDev != nil {
		// TODO(mark) remove once cookieless_dev is phased out
		env.AuthConfig.SessionSettings.URLBasedSessionSyncing = *params.CookielessDev
		authConfigColumns.Insert(sqbmodel.AuthConfigColumns.SessionSettings)
	}

	if params.DevelopmentOrigin != nil {
		if env.Instance.IsDevelopment() {
			err := validateURL(*params.DevelopmentOrigin, "development_origin")
			if err != nil {
				return err
			}

			env.Domain.DevelopmentOrigin = null.StringFrom(*params.DevelopmentOrigin)
			domainColumns = append(domainColumns, sqbmodel.DomainColumns.DevelopmentOrigin)
		} else {
			return apierror.FormParameterNotAllowedConditionally("development_origin", "environment", "production")
		}
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		if instanceColumns.Count() > 0 {
			err := s.instanceRepo.Update(ctx, txEmitter, env.Instance, instanceColumns.Array()...)
			if err != nil {
				return true, err
			}
		}

		if authConfigColumns.Count() > 0 {
			err := s.authConfigRepo.Update(ctx, txEmitter, env.AuthConfig, authConfigColumns.Array()...)
			if err != nil {
				return true, err
			}
		}

		if len(displayConfigColumns) > 0 {
			err := s.displayConfigRepo.Update(ctx, txEmitter, env.DisplayConfig, displayConfigColumns...)
			if err != nil {
				return true, err
			}
		}

		if len(domainColumns) > 0 {
			err := s.domainRepo.Update(ctx, txEmitter, env.Domain, domainColumns...)
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
		return apierror.Unexpected(txErr)
	}
	return nil
}

type UpdateRestrictionsParams struct {
	Allowlist                   *bool `json:"allowlist" form:"allowlist"`
	Blocklist                   *bool `json:"blocklist" form:"blocklist"`
	BlockEmailSubaddresses      *bool `json:"block_email_subaddresses" form:"block_email_subaddresses"`
	BlockDisposableEmailDomains *bool `json:"block_disposable_email_domains" form:"block_disposable_email_domains"`
	IgnoreDotsForGmailAddresses *bool `json:"ignore_dots_for_gmail_addresses" form:"ignore_dots_for_gmail_addresses"`
}

func (s *Service) UpdateRestrictions(ctx context.Context, params UpdateRestrictionsParams) (*serialize.InstanceRestrictionsResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	authConfig := env.AuthConfig

	if params.Allowlist != nil {
		authConfig.UserSettings.Restrictions.Allowlist.Enabled = *params.Allowlist
	}
	if params.Blocklist != nil {
		authConfig.UserSettings.Restrictions.Blocklist.Enabled = *params.Blocklist
	}
	if params.BlockEmailSubaddresses != nil {
		authConfig.UserSettings.Restrictions.BlockEmailSubaddresses.Enabled = *params.BlockEmailSubaddresses
		// IgnoreDotsForGmailAddresses is a subsetting of BlockEmailSubaddresses
		authConfig.UserSettings.Restrictions.IgnoreDotsForGmailAddresses.Enabled = *params.BlockEmailSubaddresses
	}
	if params.BlockDisposableEmailDomains != nil {
		authConfig.UserSettings.Restrictions.BlockDisposableEmailDomains.Enabled = *params.BlockDisposableEmailDomains
	}
	if params.IgnoreDotsForGmailAddresses != nil {
		if !authConfig.UserSettings.Restrictions.BlockEmailSubaddresses.Enabled {
			return nil, apierror.FormParameterNotAllowedConditionally("ignore_dots_for_gmail_addresses", "block_email_subaddresses", "false")
		}

		authConfig.UserSettings.Restrictions.IgnoreDotsForGmailAddresses.Enabled = *params.IgnoreDotsForGmailAddresses
	}

	if !env.Instance.HasAccessToAllFeatures() {
		plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		features := billing.UserSettingsFeatures(usersettings.NewUserSettings(authConfig.UserSettings))
		unsupportedFeatures := billing.ValidateSupportedFeatures(features, env.Subscription, plans...)
		if len(unsupportedFeatures) > 0 {
			return nil, apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
		}
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, authConfig)
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.InstanceRestrictions(authConfig.UserSettings), nil
}

// CreateDemoInstance creates an application with a development instance, that's
// not owned by any user (i.e. unclaimed) and is subject to future deletion.
func (s *Service) CreateDemoInstance(ctx context.Context) (*serialize.DemoDevInstanceResponse, apierror.Error) {
	freePlan, err := s.subscriptionPlanRepo.FindFirstAvailableAndFreeByResourceType(ctx, s.db, constants.ApplicationResource)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	authConfigOpts := []func(*model.AuthConfig){
		generate.WithEmailAddress(true, set.New(constants.VSEmailCode), true, set.New(constants.VSEmailCode), true),
		generate.WithUsername(true, true),
		generate.WithPassword(true),
	}

	if oauth.ProviderExists(provider.GoogleID()) {
		blockEmailSubaddresses := cenv.IsEnabled(cenv.FlagOAuthBlockEmailSubaddresses)
		authConfigOpts = append(authConfigOpts, generate.WithOAuth(provider.GoogleID(), false, true, blockEmailSubaddresses))
	}

	var stack *generate.Stack

	txerr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		color := model.DefaultThemeColor

		stack, err = generate.NewStack(ctx, txEmitter, s.gueClient, &generate.StackOptions{
			ApplicationName:   "My First Application",
			AuthConfigOptions: authConfigOpts,
			DisplayConfig: model.DisplayConfigOpts{
				Color:          &color,
				ClerkJSVersion: model.DisplayConfigClass.DefaultClerkJSVersion(),
			},
			EnvironmentType: constants.ETDevelopment,
			Plan:            freePlan,
			ResourceType:    constants.RTUser,
		})
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txerr != nil {
		return nil, apierror.Unexpected(txerr)
	}

	return serialize.DemoDevInstance(stack.Instance, stack.Domain, stack.InstanceKey), nil
}

type UpdateOrganizationSettingsParams struct {
	Enabled                *bool    `json:"enabled" form:"enabled"`
	MaxAllowedMemberships  *int     `json:"max_allowed_memberships" form:"max_allowed_memberships" validate:"omitempty,numeric,gte=0"`
	AdminDeleteEnabled     *bool    `json:"admin_delete_enabled" form:"admin_delete_enabled"`
	DomainsEnabled         *bool    `json:"domains_enabled" form:"domains_enabled"`
	DomainsEnrollmentModes []string `json:"domains_enrollment_modes" form:"domains_enrollment_modes"`
	CreatorRoleID          *string  `json:"creator_role_id" form:"creator_role_id"`
	DomainsDefaultRoleID   *string  `json:"domains_default_role_id" form:"domains_default_role_id"`
}

func (p UpdateOrganizationSettingsParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	if p.MaxAllowedMemberships != nil && *p.MaxAllowedMemberships > math.MaxInt32 {
		return apierror.FormParameterValueTooLarge("max_allowed_memberships", *p.MaxAllowedMemberships)
	}

	for _, mode := range p.DomainsEnrollmentModes {
		if !constants.OrganizationDomainEnrollmentModes.Contains(mode) {
			return apierror.FormInvalidParameterValueWithAllowed("domains_enrollment_modes", mode, constants.OrganizationDomainEnrollmentModes.Array())
		}
	}

	// If you try to enable the domains feature, you are also required to provide the default role ID
	if p.DomainsEnabled != nil && *p.DomainsEnabled && (p.DomainsDefaultRoleID == nil || *p.DomainsDefaultRoleID == "") {
		return apierror.FormMissingConditionalParameterOnExistence("domains_default_role_id", "domains_enabled")
	}

	return nil
}

func (s *Service) UpdateOrganizationSettings(ctx context.Context, params UpdateOrganizationSettingsParams) (*serialize.OrganizationSettingsResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	authConfig := env.AuthConfig

	plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if apiErr := s.validateUpdateOrganizationSettingsParams(ctx, env, params, plans); apiErr != nil {
		return nil, apiErr
	}

	if params.Enabled != nil {
		authConfig.OrganizationSettings.Enabled = *params.Enabled
	}

	maxAllowedMemberships := s.calculateMaxAllowedMemberships(env, params, plans)
	if maxAllowedMemberships != nil {
		authConfig.OrganizationSettings.MaxAllowedMemberships = *maxAllowedMemberships
	}

	if params.AdminDeleteEnabled != nil {
		authConfig.OrganizationSettings.Actions.AdminDelete = *params.AdminDeleteEnabled
	}

	if params.DomainsEnabled != nil {
		authConfig.OrganizationSettings.Domains.Enabled = *params.DomainsEnabled
		if !authConfig.IsOrganizationDomainsEnabled() {
			// If organization domains are getting disabled, make sure to also remove the default role
			authConfig.OrganizationSettings.Domains.DefaultRole = ""
		}
	}

	if len(params.DomainsEnrollmentModes) > 0 {
		// Make sure to also include the default 'manual_invitation' mode always
		enrollmentModes := set.New(constants.EnrollmentModeManualInvitation)
		enrollmentModes.Insert(params.DomainsEnrollmentModes...)
		authConfig.OrganizationSettings.Domains.EnrollmentModes = enrollmentModes.Array()
	}

	if params.CreatorRoleID != nil {
		creatorRole, err := s.roleRepo.FindByIDAndInstance(ctx, s.db, *params.CreatorRoleID, env.Instance.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		authConfig.OrganizationSettings.CreatorRole = creatorRole.Key
	}

	if authConfig.IsOrganizationDomainsEnabled() && params.DomainsDefaultRoleID != nil {
		domainDefaultRole, err := s.roleRepo.QueryByIDAndInstance(ctx, s.db, *params.DomainsDefaultRoleID, env.Instance.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		if domainDefaultRole == nil {
			return nil, apierror.OrganizationRoleNotFound("domains_default_role_id")
		}

		authConfig.OrganizationSettings.Domains.DefaultRole = domainDefaultRole.Key
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		if err := s.authConfigRepo.UpdateOrganizationSettings(ctx, txEmitter, authConfig); err != nil {
			return true, err
		}

		if params.Enabled != nil && *params.Enabled {
			if err := s.organizationsService.CreateDefaultRolesAndPermissions(ctx, txEmitter, env.Instance.ID); err != nil {
				return true, err
			}
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationSettings(authConfig.OrganizationSettings), nil
}

func (s *Service) validateUpdateOrganizationSettingsParams(ctx context.Context, env *model.Env, params UpdateOrganizationSettingsParams, plans []*model.SubscriptionPlan) apierror.Error {
	if apiErr := params.validate(s.validator); apiErr != nil {
		return apiErr
	}

	if !env.Instance.HasAccessToAllFeatures() {
		if params.MaxAllowedMemberships != nil {
			unsupportedFeature := billing.ValidateAllowedMemberships(*params.MaxAllowedMemberships, plans...)
			if unsupportedFeature != "" {
				return apierror.UnsupportedSubscriptionPlanFeatures([]string{unsupportedFeature})
			}
		}

		settings := env.AuthConfig.OrganizationSettings
		if params.DomainsEnabled != nil {
			settings.Domains.Enabled = *params.DomainsEnabled
		}
		unsupportedFeatures := billing.ValidateSupportedFeatures(
			billing.OrganizationFeatures(settings, env.Instance.CreatedAt),
			env.Subscription,
			plans...,
		)
		if len(unsupportedFeatures) > 0 {
			return apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
		}
	}

	if params.CreatorRoleID != nil {
		return s.validateCreatorRolePermissions(ctx, *params.CreatorRoleID, env.Instance.ID)
	}

	return nil
}

func (s *Service) calculateMaxAllowedMemberships(env *model.Env, params UpdateOrganizationSettingsParams, plans []*model.SubscriptionPlan) *int {
	if env.Instance.HasAccessToAllFeatures() {
		return params.MaxAllowedMemberships
	}

	currentSetting := null.IntFrom(env.AuthConfig.OrganizationSettings.MaxAllowedMemberships).Ptr()
	if params.MaxAllowedMemberships == nil && params.Enabled == nil {
		return currentSetting
	}

	newMaxAllowedMemberships := currentSetting
	if params.MaxAllowedMemberships != nil {
		newMaxAllowedMemberships = params.MaxAllowedMemberships
	}

	maxPlanAllowedMemberships := model.MaxAllowedOrganizationMemberships(plans)

	// apply plan's limit if it's more restrictive than the new setting
	if newMaxAllowedMemberships != nil &&
		maxPlanAllowedMemberships != model.UnlimitedMemberships &&
		(*newMaxAllowedMemberships == model.UnlimitedMemberships || *newMaxAllowedMemberships > maxPlanAllowedMemberships) {
		return &maxPlanAllowedMemberships
	}

	return newMaxAllowedMemberships
}

func (s *Service) validateCreatorRolePermissions(ctx context.Context, creatorRoleID, instanceID string) apierror.Error {
	role, err := s.roleRepo.QueryByIDAndInstance(ctx, s.db, creatorRoleID, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if role == nil {
		return apierror.OrganizationRoleNotFound(param.CreatorRole.Name)
	}

	permissions, err := s.permissionRepo.FindAllByRole(ctx, s.db, role.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	return s.organizationsService.EnsureMinimumSystemPermissions(permissions)
}

type UpdateHomeURLParams struct {
	HomeURL string `json:"home_url" form:"home_url"`
}

func (s *Service) UpdateHomeURL(
	ctx context.Context,
	p UpdateHomeURLParams,
) apierror.Error {
	env := environment.FromContext(ctx)

	if !env.Instance.IsProduction() {
		return apierror.DomainUpdateForbidden()
	}

	urlInfo, err := url.Analyze(p.HomeURL)
	if err != nil {
		return apierror.FormInvalidParameterValue(param.HomeURL.Name, p.HomeURL)
	}
	apiErr := clerkValidators.DomainName(clerkValidators.DomainNameInput{
		URLInfo:       urlInfo,
		IsDevelopment: !env.Instance.IsProduction(),
		IsSatellite:   env.Domain.IsSatellite(env.Instance),
		ProxyURL:      env.Domain.ProxyURL.Ptr(),
		ParamName:     param.HomeURL.Name,
	})
	if apiErr != nil {
		return apiErr
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		env.Instance.HomeOrigin = null.StringFrom(urlInfo.Origin)
		if err := s.instanceRepo.UpdateHomeOrigin(ctx, txEmitter, env.Instance); err != nil {
			return true, err
		}

		// Return if the domain hasn't changed.
		// This happens when we performing a subdomain only change.
		if env.Domain.Name == urlInfo.Domain {
			return false, nil
		}

		if err := s.domainService.Delete(ctx, txEmitter, env.Domain); err != nil {
			return true, err
		}

		newDomain, err := generate.InstanceActiveDomain(ctx, txEmitter, s.gueClient, env.Instance, urlInfo.Domain)
		if err != nil {
			return true, err
		}

		if _, err := generate.DNSCheck(ctx, txEmitter, env.Instance, newDomain); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueDomainName) {
			return apierror.HomeURLTaken(urlInfo.Domain, param.HomeURL.Name)
		}
		return apierror.Unexpected(txErr)
	}
	return nil
}
