package serialize

import (
	"context"

	"clerk/model"
	"clerk/pkg/externalapis/clerkimages"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/time"
)

type MinimalApplicationResponse struct {
	Object              string                              `json:"object"`
	ID                  string                              `json:"id"`
	Name                string                              `json:"name"`
	CardBackgroundColor string                              `json:"card_background_color"`
	CardFontFamily      string                              `json:"card_font_family"`
	IntegrationTypes    []string                            `json:"integration_types"`
	LogoImageURL        *string                             `json:"logo_image_url,omitempty"`
	FaviconImageURL     *string                             `json:"favicon_image_url,omitempty"`
	Instances           []*MinimalInstanceDashboardResponse `json:"instances,omitempty"`
	CreatedAt           int64                               `json:"created_at"`
	UpdatedAt           int64                               `json:"updated_at"`

	// TODO(2023-01-23, agis): remove this when we always show the new API keys
	// to everyone, no matter what
	ShowNewAPIKeys    bool `json:"show_new_api_keys"`
	ShowLegacyAPIKeys bool `json:"show_legacy_api_keys"`

	// TODO(account_portal) Remove once available for all
	AccountPortalAllowed bool `json:"account_portal_allowed"`
}

type ExtendedApplicationResponse struct {
	*MinimalApplicationResponse
	Instances                   []*InstanceResponse `json:"instances,omitempty"`
	SubscriptionPlanTitle       string              `json:"subscription_plan_title"` // DX: Deprecated
	UserAccessibleFeatures      []string            `json:"user_accessible_features"`
	SubscriptionTrialDays       int                 `json:"subscription_trial_days,omitempty"`
	HasActiveProductionInstance bool                `json:"has_active_production_instance"`
}

func MinimalApplication(ctx context.Context, app *model.ApplicationSerializableMinimal) *MinimalApplicationResponse {
	logoImageURL, err := clerkimages.GenerateImageURL(clerkimages.NewProxyOptions(app.LogoPublicURL.Ptr()))
	// This error should never happen, but if it happens
	// we add this notification and return empty string as ImageURL
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}
	faviconImageURL, err := clerkimages.GenerateImageURL(clerkimages.NewProxyOptions(app.FaviconPublicURL.Ptr()))
	// This error should never happen, but if it happens
	// we add this notification and return empty string as ImageURL
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}
	response := MinimalApplicationResponse{
		Object:               "application",
		ID:                   app.ID,
		Name:                 app.Name,
		CardBackgroundColor:  app.DisplayConfig.GetGeneralColor(),
		CardFontFamily:       app.DisplayConfig.GetGeneralFontFamily(),
		IntegrationTypes:     app.IntegrationTypes,
		LogoImageURL:         &logoImageURL,
		FaviconImageURL:      &faviconImageURL,
		ShowNewAPIKeys:       false,
		AccountPortalAllowed: app.AccountPortalAllowed,
		CreatedAt:            time.UnixMilli(app.CreatedAt),
		UpdatedAt:            time.UnixMilli(app.UpdatedAt),
	}

	response.Instances = make([]*MinimalInstanceDashboardResponse, len(app.Instances))
	for i, instance := range app.Instances {
		response.Instances[i] = MinimalInstanceDashboard(instance)

		if !response.ShowNewAPIKeys {
			response.ShowNewAPIKeys = instance.UsesKimaKeys()
		}
	}

	response.ShowLegacyAPIKeys = !response.ShowNewAPIKeys

	return &response
}

func ExtendedApplication(ctx context.Context, app *model.ApplicationSerializable) *ExtendedApplicationResponse {
	response := &ExtendedApplicationResponse{
		MinimalApplicationResponse:  MinimalApplication(ctx, app.ApplicationSerializableMinimal),
		SubscriptionPlanTitle:       app.SubscriptionPlan.Title,
		UserAccessibleFeatures:      app.UserAccessibleFeatures,
		HasActiveProductionInstance: app.HasActiveProductionInstance,
	}

	if app.Subscription.TrialPeriodDays > 0 {
		response.SubscriptionTrialDays = app.Subscription.TrialPeriodDays
	}

	response.Instances = make([]*InstanceResponse, len(app.Instances))
	for i, instance := range app.Instances {
		instanceResponse := Instance(
			ctx,
			&model.Env{
				Application:   app.Application,
				Instance:      instance,
				Domain:        &model.Domain{Domain: instance.R.Domains[0]},
				AuthConfig:    &model.AuthConfig{AuthConfig: instance.R.AuthConfigs[0]},
				DisplayConfig: &model.DisplayConfig{DisplayConfig: instance.R.DisplayConfigs[0]},
				Subscription:  app.Subscription,
			},
			app.AppImages,
		)
		response.Instances[i] = instanceResponse
	}

	return response
}
