package serialize

import (
	"context"

	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/paths"
	sentryclerk "clerk/pkg/sentry"
	usersettings "clerk/pkg/usersettings/model"

	"github.com/volatiletech/null/v8"
)

type DisplayConfigResponse struct {
	Object                      string                    `json:"object"`
	ID                          string                    `json:"id"`
	InstanceEnvironmentType     constants.EnvironmentType `json:"instance_environment_type"`
	ApplicationName             string                    `json:"application_name"`
	Theme                       interface{}               `json:"theme" logger:"omit"`
	PreferredSignInStrategy     string                    `json:"preferred_sign_in_strategy"`
	LogoImageURL                *string                   `json:"logo_image_url,omitempty"`
	FaviconImageURL             *string                   `json:"favicon_image_url,omitempty"`
	HomeURL                     string                    `json:"home_url"`
	SignInURL                   string                    `json:"sign_in_url"`
	SignUpURL                   string                    `json:"sign_up_url"`
	UserProfileURL              string                    `json:"user_profile_url"`
	AfterSignInURL              string                    `json:"after_sign_in_url"`
	AfterSignUpURL              string                    `json:"after_sign_up_url"`
	AfterSignOutOneURL          string                    `json:"after_sign_out_one_url"`
	AfterSignOutAllURL          string                    `json:"after_sign_out_all_url"`
	AfterSwitchSessionURL       string                    `json:"after_switch_session_url"`
	OrganizationProfileURL      string                    `json:"organization_profile_url"`
	CreateOrganizationURL       string                    `json:"create_organization_url"`
	AfterLeaveOrganizationURL   string                    `json:"after_leave_organization_url"`
	AfterCreateOrganizationURL  string                    `json:"after_create_organization_url"`
	LogoLinkURL                 string                    `json:"logo_link_url"`
	SupportEmail                *string                   `json:"support_email"`
	Branded                     bool                      `json:"branded"`
	ExperimentalForceOAuthFirst bool                      `json:"experimental_force_oauth_first"`
	ClerkJSVersion              *string                   `json:"clerk_js_version"`

	// The Turnstile sitekey corresponding to the chosen CaptchaWidgetType, as denoted
	// by the instance's settings.
	CaptchaPublicKey  *string                        `json:"captcha_public_key"`
	CaptchaWidgetType *constants.TurnstileWidgetType `json:"captcha_widget_type"`

	// The Turnstile sitekey corresponding to the invisible widget type. Acts as
	// a fallback that the frontend may leverage if it fails to render the
	// 'smart' widget.
	CaptchaPublicKeyInvisible *string `json:"captcha_public_key_invisible"`

	GoogleOneTapClientID *string `json:"google_one_tap_client_id"`

	HelpURL          *string `json:"help_url"`
	PrivacyPolicyURL *string `json:"privacy_policy_url"`
	TermsURL         *string `json:"terms_url"`

	// Deprecated: use LogoImageURL
	LogoURL *string `json:"logo_url"`
	// Deprecated: use FaviconImageURL
	FaviconURL *string `json:"favicon_url"`
	// Deprecated: v4.2.1
	LogoImage *ImageResponse `json:"logo_image"`
	// Deprecated: v4.2.1
	FaviconImage *ImageResponse `json:"favicon_image"`
}

type DisplayConfigDashboardResponse struct {
	*DisplayConfigResponse
	HomePath                          null.String `json:"home_path"`
	DefaultHomeURL                    string      `json:"default_home_url"`
	SignInPath                        null.String `json:"sign_in_path"`
	DefaultSignInURL                  string      `json:"default_sign_in_url"`
	SignUpPath                        null.String `json:"sign_up_path"`
	DefaultSignUpURL                  string      `json:"default_sign_up_url"`
	UserProfilePath                   null.String `json:"user_profile_path"`
	DefaultUserProfileURL             string      `json:"default_user_profile_url"`
	AfterSignInPath                   null.String `json:"after_sign_in_path"`
	DefaultAfterSignInURL             string      `json:"default_after_sign_in_url"`
	AfterSignUpPath                   null.String `json:"after_sign_up_path"`
	DefaultAfterSignUpURL             string      `json:"default_after_sign_up_url"`
	AfterSignOutOnePath               null.String `json:"after_sign_out_one_path"`
	DefaultAfterSignOutOneURL         string      `json:"default_after_sign_out_one_url"`
	AfterSignOutAllPath               null.String `json:"after_sign_out_all_path"`
	DefaultAfterSignOutAllURL         string      `json:"default_after_sign_out_all_url"`
	AfterSwitchSessionPath            null.String `json:"after_switch_session_path"`
	DefaultAfterSwitchSessionURL      string      `json:"default_after_switch_session_url"`
	OrganizationProfilePath           null.String `json:"organization_profile_path"`
	DefaultOrganizationProfileURL     string      `json:"default_organization_profile_url"`
	CreateOrganizationPath            null.String `json:"create_organization_path"`
	DefaultCreateOrganizationURL      string      `json:"default_create_organization_url"`
	AfterCreateOrganizationPath       null.String `json:"after_create_organization_path"`
	DefaultAfterCreateOrganizationURL string      `json:"default_after_create_organization_url"`
	AfterLeaveOrganizationPath        null.String `json:"after_leave_organization_path"`
	DefaultAfterLeaveOrganizationURL  string      `json:"default_after_leave_organization_url"`
	LogoLinkPath                      null.String `json:"logo_link_path"`
	DefaultLogoLinkURL                string      `json:"default_logo_link_url"`
}

type DisplayConfigParams struct {
	Env                  *model.Env
	AppImages            *model.AppImages
	DevBrowser           *model.DevBrowser
	GoogleOneTapClientID *string
}

func DisplayConfig(ctx context.Context, params DisplayConfigParams) *DisplayConfigResponse {
	origin := params.Env.Instance.Origin(params.Env.Domain, params.DevBrowser)
	accountsURL := params.Env.Domain.AccountsURL()
	preferredSignInStrategy := constants.AuthenticationStrategyOTP
	if params.Env.AuthConfig.UserSettings.Attributes.Password.Required {
		preferredSignInStrategy = constants.AuthenticationStrategyPassword
	}

	logoImageOptions := clerkimages.NewProxyOptions(params.Env.Application.LogoPublicURL.Ptr())
	logoImageURL, err := clerkimages.GenerateImageURL(logoImageOptions)
	// This error should never happen, but if it happens
	// we add this notification and return empty string as ImageURL
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}

	faviconImageOptions := clerkimages.NewProxyOptions(params.Env.Application.FaviconPublicURL.Ptr())
	faviconImageURL, err := clerkimages.GenerateImageURL(faviconImageOptions)
	// This error should never happen, but if it happens
	// we add this notification and return empty string as ImageURL
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}

	res := DisplayConfigResponse{
		Object:                      "display_config",
		ID:                          params.Env.DisplayConfig.ID,
		InstanceEnvironmentType:     constants.ToEnvironmentType(params.Env.Instance.EnvironmentType),
		ApplicationName:             params.Env.Application.Name,
		HomeURL:                     params.Env.DisplayConfig.Paths.HomeURL(origin, accountsURL),
		Theme:                       params.Env.DisplayConfig.Theme,
		PreferredSignInStrategy:     preferredSignInStrategy,
		SignInURL:                   params.Env.DisplayConfig.Paths.SignInURL(origin, accountsURL),
		SignUpURL:                   params.Env.DisplayConfig.Paths.SignUpURL(origin, accountsURL),
		UserProfileURL:              params.Env.DisplayConfig.Paths.UserProfileURL(origin, accountsURL),
		AfterSignInURL:              params.Env.DisplayConfig.Paths.AfterSignInURL(origin, accountsURL),
		AfterSignUpURL:              params.Env.DisplayConfig.Paths.AfterSignUpURL(origin, accountsURL),
		AfterSignOutOneURL:          params.Env.DisplayConfig.Paths.AfterSignOutOneURL(origin, accountsURL),
		AfterSignOutAllURL:          params.Env.DisplayConfig.Paths.AfterSignOutAllURL(origin, accountsURL),
		AfterSwitchSessionURL:       params.Env.DisplayConfig.Paths.AfterSwitchSessionURL(origin, accountsURL),
		OrganizationProfileURL:      params.Env.DisplayConfig.Paths.OrganizationProfileURL(origin, accountsURL),
		CreateOrganizationURL:       params.Env.DisplayConfig.Paths.CreateOrganizationURL(origin, accountsURL),
		AfterCreateOrganizationURL:  params.Env.DisplayConfig.Paths.AfterCreateOrganizationURL(origin, accountsURL),
		AfterLeaveOrganizationURL:   params.Env.DisplayConfig.Paths.AfterLeaveOrganizationURL(origin, accountsURL),
		LogoLinkURL:                 params.Env.DisplayConfig.Paths.LogoLinkURL(origin, accountsURL),
		HelpURL:                     params.Env.DisplayConfig.ExternalLinks.HelpURL.Ptr(),
		PrivacyPolicyURL:            params.Env.DisplayConfig.ExternalLinks.PrivacyPolicyURL.Ptr(),
		TermsURL:                    params.Env.DisplayConfig.ExternalLinks.TermsURL.Ptr(),
		SupportEmail:                params.Env.Instance.Communication.SupportEmail.Ptr(),
		LogoURL:                     params.Env.Application.GetLogoURL(),
		LogoImageURL:                &logoImageURL,
		FaviconURL:                  params.Env.Application.GetFaviconURL(),
		FaviconImageURL:             &faviconImageURL,
		Branded:                     params.Env.DisplayConfig.ShowClerkBranding,
		ExperimentalForceOAuthFirst: params.Env.DisplayConfig.Experimental.ForceOAuthFirst,
		ClerkJSVersion:              params.Env.DisplayConfig.ClerkJSVersion.Ptr(),
		GoogleOneTapClientID:        params.GoogleOneTapClientID,
	}

	if params.AppImages != nil {
		res.LogoImage = Image(params.AppImages.Logo)
		res.FaviconImage = Image(params.AppImages.Favicon)
	}

	if params.Env.AuthConfig.UserSettings.SignUp.CaptchaEnabled {
		key, err := usersettings.TurnstileSiteKey(params.Env.AuthConfig.UserSettings.SignUp.CaptchaWidgetType)
		if err != nil {
			sentryclerk.CaptureException(ctx, err)
			key = cenv.Get(cenv.CloudflareTurnstileSiteKeyInvisible)
		}
		res.CaptchaPublicKey = &key

		invisibleKey := cenv.Get(cenv.CloudflareTurnstileSiteKeyInvisible)
		res.CaptchaPublicKeyInvisible = &invisibleKey
		res.CaptchaWidgetType = &params.Env.AuthConfig.UserSettings.SignUp.CaptchaWidgetType
	}

	return &res
}

type DisplayConfigDAPIParams struct {
	Env       *model.Env
	AppImages *model.AppImages
}

func DisplayConfigForDashboardAPI(ctx context.Context, params DisplayConfigDAPIParams) *DisplayConfigDashboardResponse {
	origin := params.Env.Instance.Origin(params.Env.Domain, nil)
	accountsURL := params.Env.Domain.AccountsURL()

	res := DisplayConfigDashboardResponse{
		DisplayConfigResponse: DisplayConfig(ctx, DisplayConfigParams{
			Env:       params.Env,
			AppImages: params.AppImages,
		}),
		HomePath:                          params.Env.DisplayConfig.Paths.Home,
		DefaultHomeURL:                    paths.DefaultHomeURL(origin, accountsURL),
		SignInPath:                        params.Env.DisplayConfig.Paths.SignIn,
		DefaultSignInURL:                  paths.DefaultSignInURL(accountsURL),
		SignUpPath:                        params.Env.DisplayConfig.Paths.SignUp,
		DefaultSignUpURL:                  paths.DefaultSignUpURL(accountsURL),
		UserProfilePath:                   params.Env.DisplayConfig.Paths.UserProfile,
		DefaultUserProfileURL:             paths.DefaultUserProfileURL(accountsURL),
		AfterSignInPath:                   params.Env.DisplayConfig.Paths.AfterSignIn,
		DefaultAfterSignInURL:             paths.DefaultAfterSignInURL(origin, accountsURL),
		AfterSignUpPath:                   params.Env.DisplayConfig.Paths.AfterSignUp,
		DefaultAfterSignUpURL:             paths.DefaultAfterSignUpURL(origin, accountsURL),
		AfterSignOutOnePath:               params.Env.DisplayConfig.Paths.AfterSignOutOne,
		DefaultAfterSignOutOneURL:         paths.DefaultAfterSignOutOneURL(accountsURL),
		AfterSignOutAllPath:               params.Env.DisplayConfig.Paths.AfterSignOutAll,
		DefaultAfterSignOutAllURL:         paths.DefaultAfterSignOutAllURL(accountsURL),
		AfterSwitchSessionPath:            params.Env.DisplayConfig.Paths.AfterSwitchSession,
		DefaultAfterSwitchSessionURL:      paths.DefaultAfterSwitchSessionURL(origin, accountsURL),
		OrganizationProfilePath:           params.Env.DisplayConfig.Paths.OrganizationProfile,
		DefaultOrganizationProfileURL:     paths.DefaultOrganizationProfileURL(accountsURL),
		CreateOrganizationPath:            params.Env.DisplayConfig.Paths.CreateOrganization,
		DefaultCreateOrganizationURL:      paths.DefaultCreateOrganizationURL(accountsURL),
		AfterCreateOrganizationPath:       params.Env.DisplayConfig.Paths.AfterCreateOrganization,
		DefaultAfterCreateOrganizationURL: paths.DefaultAfterCreateOrganizationURL(origin, accountsURL),
		AfterLeaveOrganizationPath:        params.Env.DisplayConfig.Paths.AfterLeaveOrganization,
		DefaultAfterLeaveOrganizationURL:  paths.DefaultAfterLeaveOrganizationURL(origin, accountsURL),
		LogoLinkPath:                      params.Env.DisplayConfig.Paths.LogoLink,
		DefaultLogoLinkURL:                paths.DefaultLogoLinkURL(origin, accountsURL),
	}

	return &res
}
