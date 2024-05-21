package display_config

import (
	"context"
	"encoding/json"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/model/sqbmodel_extensions"
	"clerk/pkg/billing"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/validator"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/params"
	"clerk/pkg/set"
	"clerk/pkg/validators"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/types"
)

type Service struct {
	db        database.Database
	gueClient *gue.Client

	// clients
	clerkImagesClient *clerkimages.Client

	// repositories
	accountPortalRepo *repository.AccountPortal
	displayConfigRepo *repository.DisplayConfig
	imageRepo         *repository.Images
	planRepo          *repository.SubscriptionPlans
}

func NewService(db database.Database, gueClient *gue.Client, clerkImagesClient *clerkimages.Client) *Service {
	return &Service{
		db:                db,
		gueClient:         gueClient,
		clerkImagesClient: clerkImagesClient,
		accountPortalRepo: repository.NewAccountPortal(),
		displayConfigRepo: repository.NewDisplayConfig(),
		imageRepo:         repository.NewImages(),
		planRepo:          repository.NewSubscriptionPlans(),
	}
}

// Read returns the display config for the given instance
func (s *Service) Read(ctx context.Context) (*serialize.DisplayConfigDashboardResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	images, err := s.imageRepo.AppImages(ctx, s.db, env.Application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.DisplayConfigForDashboardAPI(ctx, serialize.DisplayConfigDAPIParams{
		Env:       env,
		AppImages: images,
	}), nil
}

// Update updates the display config with the given values
func (s *Service) Update(ctx context.Context, instanceID string, dcSettings params.DisplayConfigSettings) (*serialize.DisplayConfigDashboardResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	validate := validator.FromContext(ctx)

	formErrs := validators.SingleSessionModeConfigPaths(env.AuthConfig.SessionSettings.SingleSessionMode, dcSettings)

	if err := validate.Struct(dcSettings); err != nil {
		formErrs = apierror.Combine(formErrs, apierror.FormValidationFailed(err))
	}

	if formErrs != nil {
		return nil, formErrs
	}

	whitelistColumns := set.New[string]()

	if dcSettings.HomePath.IsSet {
		env.DisplayConfig.Paths.Home = null.StringFromPtr(dcSettings.HomePath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.SignInPath.IsSet {
		env.DisplayConfig.Paths.SignIn = null.StringFromPtr(dcSettings.SignInPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.SignUpPath.IsSet {
		env.DisplayConfig.Paths.SignUp = null.StringFromPtr(dcSettings.SignUpPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.UserProfilePath.IsSet {
		env.DisplayConfig.Paths.UserProfile = null.StringFromPtr(dcSettings.UserProfilePath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.AfterSignInPath.IsSet {
		env.DisplayConfig.Paths.AfterSignIn = null.StringFromPtr(dcSettings.AfterSignInPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.AfterSignUpPath.IsSet {
		env.DisplayConfig.Paths.AfterSignUp = null.StringFromPtr(dcSettings.AfterSignUpPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.AfterSignOutOnePath.IsSet {
		env.DisplayConfig.Paths.AfterSignOutOne = null.StringFromPtr(dcSettings.AfterSignOutOnePath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.AfterSignOutAllPath.IsSet {
		env.DisplayConfig.Paths.AfterSignOutAll = null.StringFromPtr(dcSettings.AfterSignOutAllPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.AfterSwitchSessionPath.IsSet {
		env.DisplayConfig.Paths.AfterSwitchSession = null.StringFromPtr(dcSettings.AfterSwitchSessionPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.OrganizationProfilePath.IsSet {
		env.DisplayConfig.Paths.OrganizationProfile = null.StringFromPtr(dcSettings.OrganizationProfilePath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.CreateOrganizationPath.IsSet {
		env.DisplayConfig.Paths.CreateOrganization = null.StringFromPtr(dcSettings.CreateOrganizationPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.AfterCreateOrganizationPath.IsSet {
		env.DisplayConfig.Paths.AfterCreateOrganization = null.StringFromPtr(dcSettings.AfterCreateOrganizationPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.AfterLeaveOrganizationPath.IsSet {
		env.DisplayConfig.Paths.AfterLeaveOrganization = null.StringFromPtr(dcSettings.AfterLeaveOrganizationPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.LogoLinkPath.IsSet {
		env.DisplayConfig.Paths.LogoLink = null.StringFromPtr(dcSettings.LogoLinkPath.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.Paths)
	}

	if dcSettings.HelpURL.IsSet {
		env.DisplayConfig.ExternalLinks.HelpURL = null.StringFromPtr(dcSettings.HelpURL.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.ExternalLinks)
	}

	if dcSettings.PrivacyPolicyURL.IsSet {
		env.DisplayConfig.ExternalLinks.PrivacyPolicyURL = null.StringFromPtr(dcSettings.PrivacyPolicyURL.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.ExternalLinks)
	}

	if dcSettings.TermsURL.IsSet {
		env.DisplayConfig.ExternalLinks.TermsURL = null.StringFromPtr(dcSettings.TermsURL.Ptr())
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.ExternalLinks)
	}

	if dcSettings.Branded != nil {
		whitelistColumns.Insert(sqbmodel.DisplayConfigColumns.ShowClerkBranding)
		env.DisplayConfig.ShowClerkBranding = *dcSettings.Branded
	}

	if !env.Instance.HasAccessToAllFeatures() {
		plans, err := s.planRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		unsupportedFeatures := billing.ValidateSupportedFeatures(
			billing.CustomizationFeatures(env.DisplayConfig),
			env.Subscription,
			plans...,
		)
		if len(unsupportedFeatures) > 0 {
			return nil, apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
		}
	}

	if whitelistColumns.Count() > 0 {
		txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
			err := s.displayConfigRepo.Update(ctx, txEmitter, env.DisplayConfig, whitelistColumns.Array()...)
			if err != nil {
				return true, err
			}

			var accountPortal *model.AccountPortal
			accountPortal, err = s.accountPortalRepo.QueryByInstanceID(ctx, txEmitter, instanceID)
			if err != nil {
				return true, err
			}

			// TODO(account_portal): stop syncing writes from dc to ap once we enable the AP in the dashboard

			if accountPortal != nil && !env.Application.AccountPortalAllowed && whitelistColumns.Contains(sqbmodel.DisplayConfigColumns.Paths) {
				// Also sync display_config paths to the AP

				accountPortal.Paths.AfterSignIn = env.DisplayConfig.Paths.AfterSignIn
				accountPortal.Paths.AfterSignUp = env.DisplayConfig.Paths.AfterSignUp
				accountPortal.Paths.AfterCreateOrganization = env.DisplayConfig.Paths.AfterCreateOrganization
				accountPortal.Paths.AfterLeaveOrganization = env.DisplayConfig.Paths.AfterLeaveOrganization

				// logo_link_url is not exposed on the display config settings

				err = s.accountPortalRepo.UpdatePaths(ctx, txEmitter, accountPortal)
				if err != nil {
					return true, err
				}
			}

			return false, nil
		})
		if txErr != nil {
			return nil, apierror.Unexpected(txErr)
		}
	}

	images, err := s.imageRepo.AppImages(ctx, s.db, env.Application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.DisplayConfigForDashboardAPI(ctx, serialize.DisplayConfigDAPIParams{
		Env:       env,
		AppImages: images,
	}), nil
}

// GetTheme - Returns instance theme JSON
func (s *Service) GetTheme(ctx context.Context) (types.JSON, apierror.Error) {
	env := environment.FromContext(ctx)
	return env.DisplayConfig.Theme, nil
}

// UpdateTheme updates theme settings
func (s *Service) UpdateTheme(ctx context.Context, instanceID string, theme *model.Theme) (*model.Theme, apierror.Error) {
	env := environment.FromContext(ctx)

	validate := validator.FromContext(ctx)
	if err := validate.Struct(theme); err != nil {
		return nil, apierror.FormValidationFailed(err)
	}

	bytes, err := json.Marshal(theme)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		env.DisplayConfig.Theme = bytes
		err = s.displayConfigRepo.UpdateTheme(ctx, txEmitter, env.DisplayConfig)
		if err != nil {
			return true, err
		}

		if env.DisplayConfig.HasImageSettings() {
			err = s.clerkImagesClient.CreateAvatarSettings(ctx, instanceID, clerkimages.CreateAvatarSettingsParams{
				User:         clerkimages.GetDefaultUserImageSettings(theme.General.Color),
				Organization: clerkimages.GetDefaultOrganizationImageSettings(theme.General.Color),
			})
		}

		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return theme, nil
}

// UpdateImageSettings updates clerk image settings & syncs them with img.clerk.com
func (s *Service) UpdateImageSettings(ctx context.Context, instanceID string, imageSettings sqbmodel_extensions.DisplayConfigImageSettings) (*sqbmodel_extensions.DisplayConfigImageSettings, apierror.Error) {
	env := environment.FromContext(ctx)

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		env.DisplayConfig.ImageSettings = imageSettings
		err := s.displayConfigRepo.UpdateImageSettings(ctx, txEmitter, env.DisplayConfig)
		if err != nil {
			return true, err
		}

		err = s.clerkImagesClient.CreateAvatarSettings(ctx, instanceID, clerkimages.CreateAvatarSettingsParams{
			User:         clerkimages.AvatarSettings(imageSettings.User),
			Organization: clerkimages.AvatarSettings(imageSettings.Organization),
		})

		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return &imageSettings, nil
}

// GetImageSettings retrieves image settings or return some defaults
func (s *Service) GetImageSettings(ctx context.Context) (*sqbmodel_extensions.DisplayConfigImageSettings, apierror.Error) {
	env := environment.FromContext(ctx)

	var theme *model.Theme
	err := json.Unmarshal(env.DisplayConfig.Theme, &theme)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if env.DisplayConfig.HasImageSettings() {
		defaultUserSettings := clerkimages.GetDefaultUserImageSettings(theme.General.Color)
		defaultOrganizationSettings := clerkimages.GetDefaultOrganizationImageSettings(theme.General.Color)

		imageSettings := sqbmodel_extensions.DisplayConfigImageSettings{
			User:         sqbmodel_extensions.AvatarSettings(defaultUserSettings),
			Organization: sqbmodel_extensions.AvatarSettings(defaultOrganizationSettings),
		}

		return &imageSettings, nil
	}

	return &env.DisplayConfig.ImageSettings, nil
}
