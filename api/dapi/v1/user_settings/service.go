package user_settings

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"clerk/api/apierror"
	"clerk/api/shared/auth_config"
	"clerk/api/shared/sso"
	"clerk/api/shared/validators"
	"clerk/model"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/maps"
	"clerk/pkg/oauth"
	"clerk/pkg/oauth/provider"
	"clerk/pkg/params"
	sdkutils "clerk/pkg/sdk"
	"clerk/pkg/sessionsettings"
	"clerk/pkg/set"
	clerkstrings "clerk/pkg/strings"
	usersettings "clerk/pkg/usersettings/clerk"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/pkg/usersettings/validation"
	"clerk/repository"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/instancesettings"
	"github.com/vgarvardt/gue/v2"
)

const (
	MinimumSessionTimeToExpireSeconds      = 5 * 60             // 5 minutes
	MinimumSessionInactivityTimeoutSeconds = 5 * 60             // 5 minutes
	MaximumSessionInactivityTimeoutSeconds = 365 * 24 * 60 * 60 // 365 days
)

type Service struct {
	db           database.Database
	gueClient    *gue.Client
	newSDKConfig sdkutils.ConfigConstructor

	authConfigSvc *auth_config.Service

	// repositories
	authConfigRepo       *repository.AuthConfig
	subscriptionPlanRepo *repository.SubscriptionPlans
}

func NewService(db database.Database, gueClient *gue.Client, newSDKConfig sdkutils.ConfigConstructor) *Service {
	return &Service{
		db:                   db,
		gueClient:            gueClient,
		newSDKConfig:         newSDKConfig,
		authConfigSvc:        auth_config.NewService(),
		authConfigRepo:       repository.NewAuthConfig(),
		subscriptionPlanRepo: repository.NewSubscriptionPlans(),
	}
}

// FindInstanceUserSettings returns the user settings of a specific instance.
func (s *Service) FindInstanceUserSettings(ctx context.Context, instanceID string) (*params.UserSettings, apierror.Error) {
	env := environment.FromContext(ctx)

	settings := params.UserSettings{
		OauthCallbackURL:        env.Instance.OauthCallbackURL(env.Domain, false),
		SessionSettingsResponse: toSessionSettingsResponse(env.AuthConfig.SessionSettings),
	}

	oauthSettings, err := s.getOauthMap(ctx, env.AuthConfig)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	appleEmailSource, err := env.Domain.EmailSourceForApplePrivateRelay()
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	obfuscateSecrets := sdkutils.ActorHasLimitedAccess(ctx)
	settings.UserSettingsResponse = toUserSettingsResponse(
		usersettings.NewUserSettings(env.AuthConfig.UserSettings),
		oauthSettings,
		appleEmailSource,
		instanceID,
		obfuscateSecrets,
	)

	return &settings, nil
}

func (s *Service) UpdateSessionSettings(ctx context.Context, params UpdateSessionsParams) (*UpdateSessionsParams, apierror.Error) {
	env := environment.FromContext(ctx)

	validateErr := s.validateConfigurableSessionLifetimeSettings(params)
	if validateErr != nil {
		return nil, validateErr
	}

	if params.SingleSessionMode != nil {
		env.AuthConfig.SessionSettings.SingleSessionMode = *params.SingleSessionMode
	}

	if params.SessionTimeToExpireEnabled {
		env.AuthConfig.SessionSettings.TimeToExpire = params.SessionTimeToExpire
	} else {
		// If the user disabled this setting, set a really large timeframe instead of zero
		env.AuthConfig.SessionSettings.TimeToExpire = constants.ExpiryTimeMax
		env.AuthConfig.SessionSettings.TimeToAbandon = constants.ExpiryTimeMax
	}

	if params.SessionInactivityTimeoutEnabled {
		env.AuthConfig.SessionSettings.InactivityTimeout = params.SessionInactivityTimeout
	} else {
		env.AuthConfig.SessionSettings.InactivityTimeout = 0
	}

	// TODO(auth): Make sure to also update the session time to abandon if the new value of
	// session time to expire is greater. This is a temporary solution, until we enable
	// to directly update the session time to abandon from Dashboard UI
	if params.SessionTimeToExpireEnabled && params.SessionTimeToExpire > env.AuthConfig.SessionSettings.TimeToAbandon {
		env.AuthConfig.SessionSettings.TimeToAbandon = params.SessionTimeToExpire
	}

	if !env.Instance.HasAccessToAllFeatures() {
		// Validate session settings against the application's effective plans
		plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		unsupportedFeatures := billing.ValidateSupportedFeatures(
			billing.SessionFeatures(env.AuthConfig.SessionSettings),
			env.Subscription,
			plans...,
		)
		if len(unsupportedFeatures) > 0 {
			return nil, apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
		}
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		err := s.authConfigRepo.UpdateSessionSettings(ctx, txEmitter, env.AuthConfig)
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return &params, nil
}

// UpdateSessionsParams provides all the necessary attributes to
// represent session related configuration.
type UpdateSessionsParams struct {
	SingleSessionMode *bool `json:"single_session_mode,omitempty"`
	// SessionTimeToExpire holds the seconds after a user will be forced to sign in again even if active.
	SessionTimeToExpire        int  `json:"session_time_to_expire"`
	SessionTimeToExpireEnabled bool `json:"session_time_to_expire_enabled"`
	// SessionInactivityTimeout holds the seconds after a user will be signed out if not active.
	SessionInactivityTimeout        int  `json:"session_inactivity_timeout"`
	SessionInactivityTimeoutEnabled bool `json:"session_inactivity_timeout_enabled"`
}

func (s *Service) validateConfigurableSessionLifetimeSettings(params UpdateSessionsParams) apierror.Error {
	if !params.SessionTimeToExpireEnabled && !params.SessionInactivityTimeoutEnabled {
		return apierror.MissingConfigurableSessionLifetimeOption()
	}

	if params.SessionTimeToExpireEnabled {
		// TODO(auth): When go-validator merges the following PR https://github.com/go-playground/validator/pull/847,
		// we can move the validation for ranges in the UserSettings form as follow:
		// `validate:"min=300,excluded_if=SessionTimeToExpireEnabled false"`
		if params.SessionTimeToExpire < MinimumSessionTimeToExpireSeconds {
			return apierror.FormInvalidParameterValue("session_time_to_expire", strconv.Itoa(params.SessionTimeToExpire))
		}
	}

	if params.SessionInactivityTimeoutEnabled {
		// TODO(auth): When go-validator merges the following PR https://github.com/go-playground/validator/pull/847,
		// we can move the validation for ranges in the UserSettings form as follow:
		// `validate:"min=300,max=31536000,excluded_if=SessionInactivityTimeoutEnabled false"`
		if params.SessionInactivityTimeout < MinimumSessionInactivityTimeoutSeconds ||
			params.SessionInactivityTimeout > MaximumSessionInactivityTimeoutSeconds {
			return apierror.FormInvalidParameterValue("session_inactivity_timeout", strconv.Itoa(params.SessionInactivityTimeout))
		}
	}

	if params.SessionTimeToExpireEnabled && params.SessionInactivityTimeoutEnabled {
		if params.SessionInactivityTimeout > params.SessionTimeToExpire {
			return apierror.FormInvalidInactivityTimeoutAgainstTimeToExpire("session_inactivity_timeout")
		}
	}

	return nil
}

func (s *Service) getOauthMap(ctx context.Context, authConfig *model.AuthConfig) (map[string]*params.OAuthProviderSettings, error) {
	userSettings := usersettings.NewUserSettings(authConfig.UserSettings)

	oauthMap := make(map[string]*params.OAuthProviderSettings)
	for _, providerID := range oauth.Providers() {
		provider, err := oauth.GetProvider(providerID)
		if err != nil {
			return nil, err
		}

		config := &params.OAuthProviderSettings{
			// TODO(oauth): Temporary fix until we align the oauth providers names
			// https://www.notion.so/clerkdev/Move-OAuth-provider-related-constants-to-a-single-place-and-consolidate-IDs-6038638c5f33427ca99b7c9b4e55eb27
			Provider:         strings.TrimPrefix(providerID, "oauth_"),
			Name:             provider.Name(),
			BaseScopes:       provider.BaseScopes(),
			AdditionalScopes: []string{},
			ExtraSettings:    make(map[string]interface{}),
		}
		if config.BaseScopes == nil {
			config.BaseScopes = []string{}
		}

		providerSettings := userSettings.Social[providerID]
		if providerSettings.CustomCredentials {
			existingOauthConfig, err := sso.ActiveOauthConfigForProvider(ctx, s.db, authConfig.ID, providerID)
			if err != nil {
				return nil, err
			}

			config.ClientID = existingOauthConfig.ClientID
			config.ClientSecret = existingOauthConfig.ClientSecret
			config.AdditionalScopes = sso.ExtractAdditionalOAuthScopes(provider, existingOauthConfig.DefaultScopesArray())

			if err = json.Unmarshal(existingOauthConfig.ProviderSettings, &config.ExtraSettings); err != nil {
				return nil, err
			}
		}

		oauthMap[providerID] = config
	}

	return oauthMap, nil
}

func toSessionSettingsResponse(sessionSettings sessionsettings.SessionSettings) params.SessionSettingsResponse {
	response := params.SessionSettingsResponse{}
	response.SingleSessionMode = &sessionSettings.SingleSessionMode
	response.SessionTimeToExpire = sessionSettings.TimeToExpire
	response.SessionTimeToExpireEnabled = sessionSettings.IsSessionTimeToExpireEnabled()
	if !response.SessionTimeToExpireEnabled {
		response.SessionTimeToExpire = 0
	}
	response.SessionInactivityTimeout = sessionSettings.InactivityTimeout
	response.SessionInactivityTimeoutEnabled = sessionSettings.IsSessionInactivityTimeoutEnabled()
	return response
}

func toUserSettingsResponse(
	userSettings *usersettings.UserSettings,
	oauthSettings map[string]*params.OAuthProviderSettings,
	appleEmailSource, instanceID string,
	obfuscateSecrets bool,
) params.UserSettingsResponse {
	response := params.UserSettingsResponse{}

	response.Attributes = make(map[string]params.Attribute)
	for _, attribute := range userSettings.AllAttributes() {
		response.Attributes[attribute.Name()] = params.Attribute{
			Name:                      attribute.Name(),
			Enabled:                   attribute.Base().Enabled,
			Required:                  attribute.Base().Required,
			UsedForFirstFactor:        attribute.Base().UsedForFirstFactor,
			FirstFactors:              attribute.Base().FirstFactors,
			UsedForSecondFactor:       attribute.Base().UsedForSecondFactor,
			SecondFactors:             attribute.Base().SecondFactors,
			Verifications:             attribute.Base().Verifications,
			VerifyAtSignUp:            attribute.Base().VerifyAtSignUp,
			IsVerifiable:              attribute.IsVerifiable(),
			IsUsedOnlyForSecondFactor: attribute.UsedOnlyForSecondFactor(),
		}
	}

	response.Social = make(map[string]params.Social)
	for strategy, social := range userSettings.Social {
		socialSettings, ok := oauthSettings[strategy]
		if !ok {
			continue
		}

		baseScopes := []string{}
		if socialSettings.BaseScopes != nil {
			baseScopes = socialSettings.BaseScopes
		}

		clientSecret := socialSettings.ClientSecret
		if obfuscateSecrets {
			clientSecret = clerkstrings.Obfuscate(socialSettings.ClientSecret)
		}

		response.Social[strategy] = params.Social{
			Provider:                socialSettings.Provider,
			Name:                    socialSettings.Name,
			Enabled:                 social.Enabled,
			Required:                social.Required,
			Authenticatable:         social.Authenticatable,
			BlockEmailSubaddresses:  social.BlockEmailSubaddresses,
			Strategy:                social.Strategy,
			CustomProfile:           social.CustomCredentials,
			ClientID:                socialSettings.ClientID,
			ClientSecret:            clientSecret,
			BaseScopes:              baseScopes,
			AdditionalScopes:        socialSettings.AdditionalScopes,
			ExtraSettings:           socialSettings.ExtraSettings,
			DevCredentialsAvailable: oauth.DevCredentialsAvailable(strategy),
			NotSelectable:           social.NotSelectable,
			Deprecated:              social.Deprecated,
		}

		if providerIsApple(strategy) {
			response.Social[strategy].ExtraSettings["apple_email_source"] = appleEmailSource
		}
	}

	// we want to populate _all_ known providers in this payload. If a provider
	// was implemented after this instance was created, its
	// `auth_configs.user_settings` won't contain that provider, so we have to
	// add it this way.
	for _, pid := range oauth.Providers() {
		_, ok := response.Social[pid]
		if ok {
			continue
		}

		if !hasAccessToOAuthProvider(pid, instanceID) {
			continue
		}

		provider, _ := oauth.GetProvider(pid)

		// Skip adding deprecated providers to the user settings response,
		// if they are not already added by configuration.
		if provider.IsDeprecated() {
			continue
		}

		response.Social[pid] = params.Social{
			Provider:                strings.TrimPrefix(provider.ID(), "oauth_"),
			Name:                    provider.Name(),
			Enabled:                 false,
			Required:                false,
			Authenticatable:         false,
			BlockEmailSubaddresses:  false,
			Strategy:                provider.ID(),
			CustomProfile:           false,
			ClientID:                "",
			ClientSecret:            "",
			BaseScopes:              provider.BaseScopes(),
			AdditionalScopes:        []string{},
			ExtraSettings:           make(map[string]interface{}),
			DevCredentialsAvailable: oauth.DevCredentialsAvailable(pid),
		}

		if providerIsApple(provider.ID()) {
			response.Social[pid].ExtraSettings["apple_email_source"] = appleEmailSource
		}
	}

	response.SAML.Enabled = userSettings.SAML.Enabled

	response.SignIn = params.SignIn{
		SecondFactor: params.SecondFactor{
			Enabled:  userSettings.SecondFactors().Count() > 0,
			Required: userSettings.SignIn.SecondFactor.Required,
		},
	}

	response.SignUp = params.SignUp{
		CaptchaEnabled:       userSettings.SignUp.CaptchaEnabled,
		CustomActionRequired: userSettings.SignUp.CustomActionRequired,
		Progressive:          userSettings.SignUp.Progressive,
		DisableHIBP:          userSettings.PasswordSettings.DisableHIBP,
	}

	if userSettings.SignUp.CaptchaEnabled {
		response.SignUp.CaptchaWidgetType = &userSettings.SignUp.CaptchaWidgetType
	}

	response.Restrictions = params.Restrictions{
		Allowlist: params.Allowlist{
			Enabled: userSettings.Restrictions.Allowlist.Enabled,
		},
		Blocklist: params.Blocklist{
			Enabled: userSettings.Restrictions.Blocklist.Enabled,
		},
		BlockEmailSubaddresses: params.BlockEmailSubaddresses{
			Enabled: userSettings.Restrictions.BlockEmailSubaddresses.Enabled,
		},
		BlockDisposableEmailDomains: params.BlockDisposableEmailDomains{
			Enabled: userSettings.Restrictions.BlockDisposableEmailDomains.Enabled,
		},
		IgnoreDotsForGmailAddresses: params.IgnoreDotsForGmailAddresses{
			Enabled: userSettings.Restrictions.IgnoreDotsForGmailAddresses.Enabled,
		},
	}

	response.PasswordSettings = userSettings.PasswordSettings
	response.Actions = userSettings.Actions
	response.AttackProtection = userSettings.AttackProtection
	response.PasskeySettings = userSettings.PasskeySettings

	return response
}

// UpdateUserSettings retrieves the active auth config user settings for the
// provided instanceID and applies a patch of updates.
// If the resulting user settings are valid, they will be persisted. Otherwise,
// an error will be returned with the validation error messages.
func (s *Service) UpdateUserSettings(ctx context.Context, instanceID string, patch map[string]interface{}) (*params.UserSettingsResponse, apierror.Error) {
	// Get existing user settings
	env := environment.FromContext(ctx)
	existing := env.AuthConfig.UserSettings

	// Patch user settings
	patched, err := patchUserSettings(existing, patch)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	patched = setZeroValuesForDisabledAttributes(patched)

	userSettings := usersettings.NewUserSettings(*patched)

	// CAPTCHA cannot be enabled for development instances
	valErr := validators.ValidateCAPTCHASetting(env.Instance, userSettings)
	if valErr != nil {
		return nil, valErr
	}

	// Magic links cannot be enabled for instances with enhanced email deliverability
	valErr = validators.ValidateEnhancedEmailDeliverability(
		env.Instance.Communication.EnhancedEmailDeliverability,
		userSettings,
	)
	if valErr != nil {
		return nil, valErr
	}

	// Validate user settings integrity
	validationErrs := validation.ValidateIntegrity(userSettings)
	if !validationErrs.IsEmpty() {
		return nil, apierror.ResourceInvalid(strings.Join(validationErrs.Messages(), ", "))
	}

	// Validate user social settings compatibilities
	validationErrs = validation.ValidateSocialSettings(userSettings)
	if !validationErrs.IsEmpty() {
		return nil, apierror.ResourceInvalid(strings.Join(validationErrs.Messages(), ", "))
	}

	apiErr := s.validateAttackProtection(env.Application, userSettings.AttackProtection)
	if apiErr != nil {
		return nil, apiErr
	}

	// Validate user settings against the application's effective plans
	if apiErr := s.validateFeaturesForInstance(ctx, s.db, billing.UserSettingsFeatures(userSettings), env.Instance, env.Subscription); apiErr != nil {
		return nil, apiErr
	}

	// Fill in all auth config columns
	env.AuthConfig.UserSettings = *patched

	var response params.UserSettingsResponse
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return true, err
		}

		oauthMap, err := s.getOauthMap(ctx, env.AuthConfig)
		if err != nil {
			return true, err
		}

		obfuscateSecrets := sdkutils.ActorHasLimitedAccess(ctx)
		response = toUserSettingsResponse(usersettings.NewUserSettings(*patched), oauthMap, "", instanceID, obfuscateSecrets)

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return &response, nil
}

func (s *Service) validateAttackProtection(application *model.Application, settings usersettingsmodel.AttackProtectionSettings) apierror.Error {
	if !settings.PII.Enabled && !cenv.IsBeforeCutoff(cenv.PIIProtectionEnabledCutoffEpochTime, application.CreatedAt) {
		return apierror.InvalidUserSettings()
	}
	return nil
}

func setZeroValuesForDisabledAttributes(userSettings *usersettingsmodel.UserSettings) *usersettingsmodel.UserSettings {
	attributes := userSettings.Attributes

	ref := reflect.Indirect(reflect.ValueOf(&attributes))
	count := ref.NumField()
	for i := 0; i < count; i++ {
		field := ref.Field(i).Interface()
		attribute := field.(usersettingsmodel.Attribute)
		if attribute.Enabled {
			continue
		}
		// attribute is disabled, set it to zero value
		attribute = usersettingsmodel.Attribute{
			Enabled:             false,
			Required:            false,
			UsedForFirstFactor:  false,
			FirstFactors:        []string{},
			UsedForSecondFactor: false,
			SecondFactors:       []string{},
			Verifications:       []string{},
			VerifyAtSignUp:      false,
		}
		ref.Field(i).Set(reflect.ValueOf(attribute))
	}
	userSettings.Attributes = attributes
	return userSettings
}

type socialParams struct {
	// we assume these are always present in the payload
	Enabled                bool `json:"enabled"`
	Required               bool `json:"required"`
	Authenticatable        bool `json:"authenticatable"`
	BlockEmailSubaddresses bool `json:"block_email_subaddresses"`
	NotSelectable          bool `json:"not_selectable"`
	Deprecated             bool `json:"deprecated"`

	// while these are not
	ClientID         string                 `json:"client_id"`
	ClientSecret     string                 `json:"client_secret"`
	BaseScopes       []string               `json:"base_scopes"`
	AdditionalScopes []string               `json:"additional_scopes"`
	ExtraSettings    map[string]interface{} `json:"extra_settings"`
}

func (s socialParams) CustomProfile() bool {
	return s.ClientID != "" && s.ClientSecret != ""
}

func (s socialParams) ToUserSettings(providerID string) usersettingsmodel.SocialSettings {
	return usersettingsmodel.SocialSettings{
		Enabled:                s.Enabled,
		Required:               s.Required,
		Authenticatable:        s.Authenticatable,
		Strategy:               providerID,
		BlockEmailSubaddresses: cenv.IsEnabled(cenv.FlagOAuthBlockEmailSubaddresses) && s.BlockEmailSubaddresses,
		CustomCredentials:      s.Enabled && s.CustomProfile(),
	}
}

func (s *Service) UpdateSocial(ctx context.Context, instanceID, providerID string,
	params socialParams,
) apierror.Error {
	env := environment.FromContext(ctx)

	provider, err := oauth.GetProvider(providerID)
	if err != nil {
		return apierror.UnsupportedOauthProvider(providerID)
	}
	if !hasAccessToOAuthProvider(providerID, instanceID) {
		return apierror.FeatureNotEnabled()
	}

	// We don't allow the usage of non-custom profile for production instances and for OAuth providers we aren't able
	// to provide dev credentials.
	if (env.Instance.IsProduction() || !oauth.DevCredentialsAvailable(provider.ID())) && params.Enabled && !params.CustomProfile() {
		return apierror.MissingCustomOauthConfig(providerID)
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		env.AuthConfig.UserSettings.Social[providerID] = params.ToUserSettings(providerID)
		settings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

		// Validate user social settings compatibilities
		validationErrs := validation.ValidateSocialSettings(settings)
		if !validationErrs.IsEmpty() {
			return true, apierror.ResourceInvalid(strings.Join(validationErrs.Messages(), ", "))
		}

		// Validate user settings against the application's effective plans
		apiErr := s.validateFeaturesForInstance(
			ctx,
			txEmitter,
			billing.UserSettingsFeatures(settings),
			env.Instance,
			env.Subscription,
		)
		if apiErr != nil {
			return true, apiErr
		}

		// update auth_configs table
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return true, err
		}

		// update enabled_sso_providers + oauth_configs tables
		if !params.Enabled {
			err = sso.Disable(ctx, txEmitter, env.AuthConfig, provider)
			if err != nil {
				return true, err
			}
			return false, nil
		}

		if params.CustomProfile() {
			_, err = sso.Configure(ctx, txEmitter, env.Instance, env.AuthConfig, provider,
				params.ClientID,
				params.ClientSecret,
				params.ExtraSettings,
				params.AdditionalScopes...,
			)
		} else {
			err = sso.EnableDevConfiguration(ctx, txEmitter, env.AuthConfig, providerID)
		}
		if err != nil {
			return true, err
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

// Apply the patch to the provided UserSettings and return the updated
// UserSettings object. Any key from patch that will overwrite the
// matching UserSettings attribute.
func patchUserSettings(us usersettingsmodel.UserSettings, patch map[string]interface{}) (*usersettingsmodel.UserSettings, error) {
	// Transform user settings to a generic map, so we can apply the patch.
	srcJSON, err := json.Marshal(us)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal user settings: %w", err)
	}
	var src map[string]interface{}
	err = json.Unmarshal(srcJSON, &src)
	if err != nil {
		return nil, fmt.Errorf("cannot unmarshal user settings: %w", err)
	}

	// Apply the patch
	merged, err := maps.Merge(src, patch)
	if err != nil {
		return nil, fmt.Errorf("cannot apply user settings patch: %w", err)
	}

	// Transform the patched map back to UserSettings
	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal patched user settings: %w", err)
	}
	var patched usersettingsmodel.UserSettings
	err = json.Unmarshal(mergedJSON, &patched)
	if err != nil {
		return nil, fmt.Errorf("cannot unmarshal patched user settings: %w", err)
	}

	return &patched, nil
}

// UpdateRestrictions updates the restrictions settings (i.e. whether allowlists and blocklists
// are enabled) of the given instance.
func (s *Service) UpdateRestrictions(ctx context.Context, instanceID string, params instancesettings.UpdateRestrictionsParams) (*sdk.InstanceRestrictions, apierror.Error) {
	config, apiErr := sdkutils.NewConfigForInstance(ctx, s.newSDKConfig, s.db, instanceID)
	if apiErr != nil {
		return nil, apiErr
	}

	restrictions, err := instancesettings.NewClient(config).UpdateRestrictions(ctx, &params)
	if err != nil {
		return nil, sdkutils.ToAPIError(err)
	}
	return restrictions, nil
}

// SwitchToPSU migrates an instance to PSU mode
func (s Service) SwitchToPSU(ctx context.Context) (*params.UserSettingsResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	obfuscateSecrets := sdkutils.ActorHasLimitedAccess(ctx)

	oauthSettings, err := s.getOauthMap(ctx, env.AuthConfig)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if env.AuthConfig.UserSettings.SignUp.Progressive {
		response := toUserSettingsResponse(usersettings.NewUserSettings(env.AuthConfig.UserSettings), oauthSettings, "", env.Instance.ID, obfuscateSecrets)
		return &response, nil
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		s.authConfigSvc.UpdateUserSettingsWithProgressiveSignUp(env.AuthConfig, true)
		err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig)
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	response := toUserSettingsResponse(usersettings.NewUserSettings(env.AuthConfig.UserSettings), oauthSettings, "", env.Instance.ID, obfuscateSecrets)
	return &response, nil
}

func providerIsApple(id string) bool {
	return id == provider.AppleID()
}

func (s *Service) validateFeaturesForInstance(
	ctx context.Context,
	exec database.Executor,
	features set.Set[string],
	instance *model.Instance,
	subscription *model.Subscription,
) apierror.Error {
	if instance.HasAccessToAllFeatures() {
		return nil
	}

	plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, exec, subscription.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	unsupportedFeatures := billing.ValidateSupportedFeatures(features, subscription, plans...)
	if len(unsupportedFeatures) > 0 {
		return apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
	}
	return nil
}

func hasAccessToOAuthProvider(providerID, instanceID string) bool {
	if providerID != provider.ExpressenID() {
		return true
	}
	return cenv.ResourceHasAccess(cenv.FlagExpressenAllowedInstanceIDs, instanceID)
}
