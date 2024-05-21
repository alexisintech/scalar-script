package authconfig

import (
	"context"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/auth_config"
	"clerk/api/shared/validators"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	db        database.Database
	gueClient *gue.Client
	validator *validator.Validate

	// services
	authConfigSvc *auth_config.Service

	// repositories
	authConfigRepo       *repository.AuthConfig
	jwtTemplateRepo      *repository.JWTTemplate
	instanceRepo         *repository.Instances
	subscriptionPlanRepo *repository.SubscriptionPlans
}

func NewService(db database.Database, gueClient *gue.Client) *Service {
	return &Service{
		db:                   db,
		gueClient:            gueClient,
		validator:            validator.New(),
		authConfigSvc:        auth_config.NewService(),
		authConfigRepo:       repository.NewAuthConfig(),
		instanceRepo:         repository.NewInstances(),
		jwtTemplateRepo:      repository.NewJWTTemplate(),
		subscriptionPlanRepo: repository.NewSubscriptionPlans(),
	}
}

type UpdateParams struct {
	RestrictedToAllowlist       *bool   `json:"restricted_to_allowlist" form:"restricted_to_allowlist"`
	FromEmailAddress            *string `json:"from_email_address" form:"from_email_address" validate:"omitempty,alphanum"`
	ProgressiveSignUp           *bool   `json:"progressive_sign_up" form:"progressive_sign_up"`
	TestMode                    *bool   `json:"test_mode" form:"test_mode"`
	EnhancedEmailDeliverability *bool   `json:"enhanced_email_deliverability" form:"enhanced_email_deliverability"`
}

// Update the auth_config of the instance
func (s *Service) Update(ctx context.Context, params UpdateParams) (*serialize.AuthConfigResponseServer, apierror.Error) {
	env := environment.FromContext(ctx)
	authConfig := env.AuthConfig
	instance := env.Instance
	subscription := env.Subscription

	err := s.validator.Struct(params)
	if err != nil {
		return nil, apierror.FormValidationFailed(err)
	}
	if params.EnhancedEmailDeliverability != nil {
		valErr := validators.ValidateEnhancedEmailDeliverability(
			*params.EnhancedEmailDeliverability,
			usersettings.NewUserSettings(authConfig.UserSettings),
		)
		if valErr != nil {
			return nil, valErr
		}
	}

	shouldUpdateInstanceCommunication := false
	authConfigColumnsToUpdate := set.New[string]()
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		if params.RestrictedToAllowlist != nil {
			authConfig.UserSettings.Restrictions.Allowlist.Enabled = *params.RestrictedToAllowlist
			authConfigColumnsToUpdate.Insert(sqbmodel.AuthConfigColumns.UserSettings)

			if !instance.HasAccessToAllFeatures() {
				plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, txEmitter, subscription.ID)
				if err != nil {
					return true, err
				}

				userSettings := usersettings.NewUserSettings(authConfig.UserSettings)
				unsupportedFeatures := billing.ValidateSupportedFeatures(billing.UserSettingsFeatures(userSettings), subscription, plans...)
				if len(unsupportedFeatures) > 0 {
					return true, apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
				}
			}
		}

		if params.FromEmailAddress != nil {
			lower := strings.ToLower(*params.FromEmailAddress)
			params.FromEmailAddress = &lower

			instance.Communication.AuthEmailsFrom = null.StringFromPtr(params.FromEmailAddress)
			shouldUpdateInstanceCommunication = true
		}

		if params.ProgressiveSignUp != nil {
			s.authConfigSvc.UpdateUserSettingsWithProgressiveSignUp(authConfig, *params.ProgressiveSignUp)
			authConfigColumnsToUpdate.Insert(sqbmodel.AuthConfigColumns.UserSettings)
		}

		if params.TestMode != nil {
			authConfig.TestMode = *params.TestMode
			authConfigColumnsToUpdate.Insert(sqbmodel.AuthConfigColumns.TestMode)
		}

		if params.EnhancedEmailDeliverability != nil {
			instance.Communication.EnhancedEmailDeliverability = *params.EnhancedEmailDeliverability
			shouldUpdateInstanceCommunication = true
		}

		if authConfigColumnsToUpdate.Count() > 0 {
			err := s.authConfigRepo.Update(ctx, txEmitter, authConfig, authConfigColumnsToUpdate.Array()...)
			if err != nil {
				return true, err
			}
		}

		if shouldUpdateInstanceCommunication {
			err := s.instanceRepo.UpdateCommunication(ctx, txEmitter, instance)
			if err != nil {
				return true, err
			}
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.AuthConfigToServerAPI(authConfig, instance), nil
}
