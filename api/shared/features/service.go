package features

import (
	"context"
	"fmt"
	"time"

	"clerk/api/shared/instances"
	"clerk/model"
	"clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/slack"
	"clerk/pkg/jobs"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	gueClient            *gue.Client
	authConfigRepo       *repository.AuthConfig
	displayConfigRepo    *repository.DisplayConfig
	domainRepo           *repository.Domain
	identificationRepo   *repository.Identification
	instanceService      *instances.Service
	jwtTemplateRepo      *repository.JWTTemplate
	permissionRepo       *repository.Permission
	roleRepo             *repository.Role
	samlConnectionRepo   *repository.SAMLConnection
	subscriptionPlanRepo *repository.SubscriptionPlans
	templateRepo         *repository.Templates
}

func NewService(db database.Database, gueClient *gue.Client) *Service {
	return &Service{
		gueClient:            gueClient,
		authConfigRepo:       repository.NewAuthConfig(),
		displayConfigRepo:    repository.NewDisplayConfig(),
		domainRepo:           repository.NewDomain(),
		identificationRepo:   repository.NewIdentification(),
		instanceService:      instances.NewService(db, gueClient),
		jwtTemplateRepo:      repository.NewJWTTemplate(),
		permissionRepo:       repository.NewPermission(),
		roleRepo:             repository.NewRole(),
		samlConnectionRepo:   repository.NewSAMLConnection(),
		subscriptionPlanRepo: repository.NewSubscriptionPlans(),
		templateRepo:         repository.NewTemplates(),
	}
}

// UnsupportedFeatures returns an array of all features that the
// given instance is using which are unsupported based on the given subscription plan.
func (s *Service) UnsupportedFeatures(
	ctx context.Context,
	exec database.Executor,
	env *model.Env,
	instanceCreatedAt time.Time,
	plans ...*model.SubscriptionPlan,
) ([]string, error) {
	allUnsupportedFeatures := set.New[string]()

	// Check if there are any custom templates (email, SMS) for the instance
	// and validate that they're supported by the plan.
	for _, templateType := range []string{string(constants.TTEmail), string(constants.TTSMS)} {
		exists, err := s.templateRepo.ExistsUserByTemplateTypeAndInstance(ctx, exec, templateType, env.Instance.ID)
		if err != nil {
			return nil, err
		}
		if exists {
			feat, err := billing.TemplateFeatures(templateType)
			if err != nil {
				return nil, err
			}
			allUnsupportedFeatures.Insert(billing.ValidateSupportedFeatures(feat, env.Subscription, plans...)...)
		}
	}

	domains, err := s.domainRepo.FindAllByInstanceID(ctx, exec, env.Instance.ID)
	if err != nil {
		return nil, err
	}
	if len(domains) > 1 {
		allUnsupportedFeatures.Insert(billing.ValidateSupportedFeatures(billing.MultiDomainFeatures(instanceCreatedAt), env.Subscription, plans...)...)
	}

	hasCustomPermissions, err := s.permissionRepo.ExistsByInstanceAndType(ctx, exec, env.Instance.ID, constants.RTUser)
	if err != nil {
		return nil, err
	}
	if hasCustomPermissions {
		allUnsupportedFeatures.Insert(billing.ValidateSupportedFeatures(billing.CustomOrganizationPermissionsFeatures(env.AuthConfig.OrganizationSettings, instanceCreatedAt), env.Subscription, plans...)...)
	}

	// The custom organization roles feature is considered in-use if
	// there are more than the default roles for the instance, or one of
	// the instance roles has custom permissions.
	totalRoles, err := s.roleRepo.CountByInstance(ctx, exec, env.Instance.ID)
	if err != nil {
		return nil, err
	}
	hasCustomRoles := totalRoles > int64(len(model.DefaultRoles()))
	if !hasCustomRoles {
		hasCustomRoles, err = s.roleRepo.ExistsWithNonSystemPermissionByInstance(ctx, exec, env.Instance.ID)
		if err != nil {
			return nil, err
		}
	}
	if hasCustomRoles {
		allUnsupportedFeatures.Insert(billing.ValidateSupportedFeatures(billing.CustomOrganizationRolesFeatures(env.AuthConfig.OrganizationSettings, instanceCreatedAt), env.Subscription, plans...)...)
	}

	// Validate the auth config settings against plan features
	for _, features := range []set.Set[string]{
		billing.UserSettingsFeatures(usersettings.NewUserSettings(env.AuthConfig.UserSettings)),
		billing.SessionFeatures(env.AuthConfig.SessionSettings),
		billing.OrganizationFeatures(env.AuthConfig.OrganizationSettings, instanceCreatedAt),
	} {
		allUnsupportedFeatures.Insert(billing.ValidateSupportedFeatures(features, env.Subscription, plans...)...)
	}

	if env.AuthConfig.OrganizationSettings.Enabled {
		unsupportedAllowedMemberships := billing.ValidateAllowedMemberships(env.AuthConfig.OrganizationSettings.MaxAllowedMemberships, plans...)
		if unsupportedAllowedMemberships != "" {
			allUnsupportedFeatures.Insert(billing.ValidateSupportedFeatures(set.New(unsupportedAllowedMemberships), env.Subscription, plans...)...)
		}
	}

	// Validate display config settings against plan features
	allUnsupportedFeatures.Insert(billing.ValidateSupportedFeatures(billing.CustomizationFeatures(env.DisplayConfig), env.Subscription, plans...)...)

	// Check whether instance has any JWT template features that are
	// not supported by the plan
	jwtTemplates, err := s.jwtTemplateRepo.FindAllByInstance(ctx, exec, env.Instance.ID)
	if err != nil {
		return nil, err
	}
	allUnsupportedFeatures.Insert(billing.ValidateSupportedFeatures(billing.JWTTemplateFeatures(jwtTemplates), env.Subscription, plans...)...)
	return allUnsupportedFeatures.Array(), nil
}

// Disable will disable the given feature from the instance.
func (s *Service) Disable(ctx context.Context, txEmitter database.TxEmitter, env *model.Env, feature string) error {
	switch feature {
	case billing.Features.AllowedMemberships:
		subscriptionPlans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, txEmitter, env.Subscription.ID)
		if err != nil {
			return err
		}
		env.AuthConfig.OrganizationSettings.MaxAllowedMemberships = model.MaxAllowedOrganizationMemberships(subscriptionPlans)
		if err := s.authConfigRepo.UpdateOrganizationSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.Allowlist:
		env.AuthConfig.UserSettings.Restrictions.Allowlist.Enabled = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.ApplicationDomains:
		err := jobs.SendSlackAlert(ctx, s.gueClient, jobs.SlackAlertArgs{
			Webhook: constants.SlackBilling,
			Message: slack.Message{
				Title: "Grace period expired but application still uses multi-domains",
				Text: fmt.Sprintf("Grace period expired for '%s' (%s), but still uses multi-domains and needs to be notified that they will be switched off.",
					env.Application.Name, env.Application.ID),
				Type: slack.Error,
			},
		}, jobs.WithTx(txEmitter))
		if err != nil {
			return err
		}

	case billing.Features.BanUser:
		// this is checked by middlewares against the subscription plan, so nothing to do here
		return nil

	case billing.Features.BlockDisposableEmailDomains:
		env.AuthConfig.UserSettings.Restrictions.BlockDisposableEmailDomains.Enabled = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.IgnoreDotsForGmailAddresses:
		env.AuthConfig.UserSettings.Restrictions.IgnoreDotsForGmailAddresses.Enabled = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.BlockEmailSubaddresses:
		env.AuthConfig.UserSettings.Restrictions.BlockEmailSubaddresses.Enabled = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.Blocklist:
		env.AuthConfig.UserSettings.Restrictions.Blocklist.Enabled = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.CustomEmailTemplate:
		if err := s.templateRepo.DeleteByInstanceID(ctx, txEmitter, env.Instance.ID); err != nil {
			return err
		}

	case billing.Features.CustomJWTTemplate:
		if err := s.jwtTemplateRepo.DeleteByInstanceID(ctx, txEmitter, env.Instance.ID); err != nil {
			return err
		}

	case billing.Features.CustomOrganizationPermissions:
		err := jobs.SendSlackAlert(ctx, s.gueClient, jobs.SlackAlertArgs{
			Webhook: constants.SlackBilling,
			Message: slack.Message{
				Title: "Grace period expired but application still uses custom permissions",
				Text: fmt.Sprintf("Grace period expired for '%s' (%s), but still uses custom organization permissions and needs to be notified that they will be switched off.",
					env.Application.Name, env.Application.ID),
				Type: slack.Error,
			},
		}, jobs.WithTx(txEmitter))
		if err != nil {
			return err
		}

	case billing.Features.CustomOrganizationRoles:
		err := jobs.SendSlackAlert(ctx, s.gueClient, jobs.SlackAlertArgs{
			Webhook: constants.SlackBilling,
			Message: slack.Message{
				Title: "Grace period expired but application still uses custom roles",
				Text: fmt.Sprintf("Grace period expired for '%s' (%s), but still uses custom organization roles and needs to be notified that they will be switched off.",
					env.Application.Name, env.Application.ID),
				Type: slack.Error,
			},
		}, jobs.WithTx(txEmitter))
		if err != nil {
			return err
		}

	case billing.Features.CustomSessionDuration:
		env.AuthConfig.SessionSettings.TimeToExpire = constants.ExpiryTimeMedium
		if err := s.authConfigRepo.UpdateSessionSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.CustomSessionInactivityTimeout:
		env.AuthConfig.SessionSettings.InactivityTimeout = 0
		if err := s.authConfigRepo.UpdateSessionSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.CustomSessionToken:
		// make sure JWT templates are not being used
		instance := env.Instance
		if instance.SessionTokenTemplateID.Valid {
			if err := s.instanceService.UpdateSessionTokenTemplateID(ctx, txEmitter, instance, nil); err != nil {
				return err
			}
		}

		if err := s.jwtTemplateRepo.DeleteByInstanceIDAndName(ctx, txEmitter, env.Instance.ID, constants.SessionTokenJWTTemplateName); err != nil {
			return err
		}

	case billing.Features.CustomSMSTemplate:
		if err := s.templateRepo.DeleteByInstanceID(ctx, txEmitter, env.Instance.ID); err != nil {
			return err
		}

	case billing.Features.DeviceTracking:
		// this is checked by middlewares against the subscription plan, so nothing to do here
		return nil

	case billing.Features.EmailCode:
		firstFactors := set.New(env.AuthConfig.UserSettings.Attributes.EmailAddress.FirstFactors...)
		firstFactors.Remove(constants.VSEmailCode)
		env.AuthConfig.UserSettings.Attributes.EmailAddress.FirstFactors = firstFactors.Array()
		if firstFactors.IsEmpty() {
			env.AuthConfig.UserSettings.Attributes.EmailAddress.UsedForFirstFactor = false
		}
		verifications := set.New(env.AuthConfig.UserSettings.Attributes.EmailAddress.Verifications...)
		verifications.Remove(constants.VSEmailCode)
		env.AuthConfig.UserSettings.Attributes.EmailAddress.Verifications = verifications.Array()
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.Impersonation:
		// this is checked by middlewares against the subscription plan, so nothing to do here
		return nil

	case billing.Features.MagicLink:
		firstFactors := set.New(env.AuthConfig.UserSettings.Attributes.EmailAddress.FirstFactors...)
		firstFactors.Remove(constants.VSEmailLink)
		env.AuthConfig.UserSettings.Attributes.EmailAddress.FirstFactors = firstFactors.Array()
		if firstFactors.IsEmpty() {
			env.AuthConfig.UserSettings.Attributes.EmailAddress.UsedForFirstFactor = false
		}
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.MFABackupCode:
		env.AuthConfig.UserSettings.Attributes.BackupCode = usersettingsmodel.EmptyAttribute()
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.MFAPhoneCode:
		env.AuthConfig.UserSettings.Attributes.PhoneNumber.SecondFactors = []string{}
		env.AuthConfig.UserSettings.Attributes.PhoneNumber.UsedForSecondFactor = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.MFATOTP:
		env.AuthConfig.UserSettings.Attributes.AuthenticatorApp = usersettingsmodel.EmptyAttribute()
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.MultiSession:
		env.AuthConfig.SessionSettings.SingleSessionMode = true
		if err := s.authConfigRepo.UpdateSessionSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.OrganizationDomains:
		env.AuthConfig.OrganizationSettings.Domains.Enabled = false
		if err := s.authConfigRepo.UpdateOrganizationSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.Passkey:
		env.AuthConfig.UserSettings.Attributes.Passkey = usersettingsmodel.EmptyAttribute()
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.Password:
		env.AuthConfig.UserSettings.Attributes.Password.Required = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.PasswordComplexity:
		env.AuthConfig.UserSettings.PasswordSettings.RequireLowercase = false
		env.AuthConfig.UserSettings.PasswordSettings.RequireUppercase = false
		env.AuthConfig.UserSettings.PasswordSettings.RequireNumbers = false
		env.AuthConfig.UserSettings.PasswordSettings.RequireSpecialChar = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.PhoneCode:
		env.AuthConfig.UserSettings.Attributes.PhoneNumber = usersettingsmodel.EmptyAttribute()
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	case billing.Features.RemoveBranding:
		env.DisplayConfig.ShowClerkBranding = true
		if err := s.displayConfigRepo.UpdateShowClerkBranding(ctx, txEmitter, env.DisplayConfig); err != nil {
			return err
		}

	case billing.Features.SAML:
		env.AuthConfig.UserSettings.SAML.Enabled = false
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}
		// disable all active SAML connections of instance
		if err := s.samlConnectionRepo.DisableAllActiveByInstance(ctx, txEmitter, env.Instance.ID); err != nil {
			return err
		}

	case billing.Features.UnlimitedSocial:
		maxSocialToRetain := 3
		socialToRetain, err := s.identificationRepo.FindSocialByPopularity(ctx, txEmitter, env.Instance.ID, maxSocialToRetain)
		if err != nil {
			return err
		}

		socialToRetainSet := set.New(socialToRetain...)
		for _, social := range env.AuthConfig.UserSettings.Social {
			if socialToRetainSet.Contains(social.Strategy) {
				continue
			}
			social.Enabled = false
			social.Required = false
			social.Authenticatable = false
		}
		if err := s.authConfigRepo.UpdateUserSettings(ctx, txEmitter, env.AuthConfig); err != nil {
			return err
		}

	default:
		return fmt.Errorf("don't know how to disable feature %s", feature)
	}
	return nil
}
