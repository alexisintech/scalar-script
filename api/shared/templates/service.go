package templates

import (
	"context"
	"fmt"
	"strings"

	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/clerkimages"
	sentryclerk "clerk/pkg/sentry"
	clerkstrings "clerk/pkg/strings"
	"clerk/pkg/templates"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	clock clockwork.Clock

	identificationRepo *repository.Identification
	signInRepo         *repository.SignIn
	signUpRepo         *repository.SignUp
	templateRepo       *repository.Templates
	userRepo           *repository.Users
	instanceRepo       *repository.Instances
}

func NewService(clock clockwork.Clock) *Service {
	return &Service{
		clock: clock,

		identificationRepo: repository.NewIdentification(),
		signInRepo:         repository.NewSignIn(),
		signUpRepo:         repository.NewSignUp(),
		templateRepo:       repository.NewTemplates(),
		userRepo:           repository.NewUsers(),
		instanceRepo:       repository.NewInstances(),
	}
}

// GetTemplate returns the template configured for the given instance, type and
// slug.
func (s *Service) GetTemplate(ctx context.Context, exec database.Executor, instanceID string, templateType constants.TemplateType, slug string) (*model.Template, error) {
	if templateType == constants.TTEmail {
		instance, err := s.instanceRepo.FindByID(ctx, exec, instanceID)
		if err != nil {
			return nil, fmt.Errorf("GetTemplate: %w", err)
		}

		if instance.Communication.EnhancedEmailDeliverability {
			// force the system template. This works because
			// templateRepo.QueryCurrentByTemplateTypeAndSlug will only return
			// system templates if no instanceID is passed.
			instanceID = ""
		}
	}

	template, err := s.templateRepo.QueryCurrentByTemplateTypeAndSlug(ctx, exec, instanceID, string(templateType), slug)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, fmt.Errorf("GetTemplate: No %s template found for slug %s on instance_id %s", templateType, slug, instanceID)
	}

	return template, nil
}

// GetCommonEmailData retrieves common data for email templates
func (s *Service) GetCommonEmailData(ctx context.Context, env *model.Env) (templates.CommonEmailData, error) {
	return templates.CommonEmailData{
		App: s.GetAppDataEmail(ctx, env),
		Theme: templates.ThemeData{
			ShowClerkBranding: env.DisplayConfig.ShowClerkBranding,
			PrimaryColor:      env.DisplayConfig.GetGeneralColor(),
			ButtonTextColor:   env.DisplayConfig.GetButtonTextColor(),
		},
	}, nil
}

// GetCommonSMSData retrieves common data for SMS templates
func (s *Service) GetCommonSMSData(ctx context.Context, env *model.Env) (templates.CommonSMSData, error) {
	return templates.CommonSMSData{App: s.GetAppData(ctx, env)}, nil
}

// GetAppData retrieves template data for an app
func (s *Service) GetAppData(_ context.Context, env *model.Env) templates.AppData {
	var appURL string
	if env.Instance.IsProduction() {
		appURL = env.Instance.HomeOrigin.String
	} else {
		appURL = env.Domain.AccountsURL()
	}

	appData := templates.AppData{
		Name:       env.Application.Name,
		URL:        appURL,
		DomainName: env.Domain.Name,
	}

	return appData
}

// GetAppDataEmail retrieves template data for an app for use in email templates
func (s *Service) GetAppDataEmail(ctx context.Context, env *model.Env) templates.AppDataEmail {
	appData := s.GetAppData(ctx, env)

	options := clerkimages.NewProxyOptions(env.Application.LogoPublicURL.Ptr())
	logoImageURL, err := clerkimages.GenerateImageURL(options)

	// This error should never happen, but if it happens
	// we add this notification and return empty string as ImageURL
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}

	appDataEmail := templates.AppDataEmail{
		AppData: appData,

		LogoURL:      env.Application.GetLogoURL(),
		LogoImageURL: &logoImageURL,
	}

	return appDataEmail
}

func (s *Service) GetCommonVerificationData(
	ctx context.Context,
	exec database.Executor,
	sourceType, sourceID, instanceID string,
) (templates.CommonVerificationData, error) {
	data := templates.CommonVerificationData{}

	switch sourceType {
	case constants.OSTSignUp:
		// TODO(templates) Consider supporting sign_up.unsafe_metadata here

		// signUp, err := s.signUpRepo.FindByID(ctx, exec, sourceID)
		// if err != nil {
		// 	return data, err
		// }
	case constants.OSTSignIn:
		signIn, err := s.signInRepo.FindByIDAndInstance(ctx, exec, sourceID, instanceID)
		if err != nil {
			return data, err
		}

		if !signIn.IdentificationID.Valid {
			return data, fmt.Errorf("GetCommonVerificationData: no identification for signIn %s: %w", signIn.ID, err)
		}

		identification, err := s.identificationRepo.FindByIDAndInstance(ctx, exec, signIn.IdentificationID.String, instanceID)
		if err != nil {
			return data, err
		}

		if !identification.UserID.Valid {
			return data, fmt.Errorf("GetCommonVerificationData: no user for identification %s: %w", identification.ID, err)
		}

		user, err := s.userRepo.FindByIDAndInstance(ctx, exec, identification.UserID.String, instanceID)
		if err != nil {
			return data, err
		}

		data.User = templates.UserToTemplateData(user)
	case constants.OSTUser:
		identification, err := s.identificationRepo.FindByIDAndInstance(ctx, exec, sourceID, instanceID)
		if err != nil {
			return data, err
		}

		if !identification.UserID.Valid {
			return data, fmt.Errorf("GetCommonVerificationData: no user for identification %s: %w", identification.ID, err)
		}

		user, err := s.userRepo.FindByIDAndInstance(ctx, exec, identification.UserID.String, instanceID)
		if err != nil {
			return data, err
		}

		data.User = templates.UserToTemplateData(user)
	default:
		panic(fmt.Sprintf("unknown default type: '%s'", sourceType))
	}

	return data, nil
}

func (s *Service) GetDeviceActivityData(deviceActivity *model.SessionActivity) templates.DeviceActivityData {
	return templates.DeviceActivityData{
		RequestedBy:   toRequestedBy(deviceActivity),
		RequestedFrom: toRequestedFrom(deviceActivity),
		RequestedAt:   toRequestedAt(s.clock),
	}
}

// FromEmailName determines the fromEmailName based on the following precedence rules:
// * template.FromEmailName, if set
// * instance.AuthEmailsFromAddressLocalPart, if applicable & set
// * fallback for template slug
func (s *Service) FromEmailName(template *model.Template, instance *model.Instance) string {
	if template.FromEmailName.Valid && template.FromEmailName.String != "" {
		return template.FromEmailName.String
	}

	switch template.Slug {
	case
		constants.VerificationCodeSlug,
		constants.MagicLinkSignInSlug,
		constants.MagicLinkSignUpSlug,
		constants.MagicLinkUserProfileSlug:
		return instance.Communication.AuthEmailsFromAddressLocalPart()
	case constants.InvitationSlug, constants.OrganizationInvitationSlug:
		return "invitations"
	default:
		return "noreply"
	}
}

func toRequestedBy(deviceActivity *model.SessionActivity) string {
	if !deviceActivity.BrowserName.Valid && deviceActivity.DeviceType.Valid {
		return deviceActivity.DeviceType.String
	} else if deviceActivity.BrowserName.Valid && !deviceActivity.DeviceType.Valid {
		return deviceActivity.BrowserName.String
	} else if !deviceActivity.BrowserName.Valid && !deviceActivity.DeviceType.Valid {
		return "an unknown device"
	}
	return fmt.Sprintf("%s, %s", deviceActivity.BrowserName.String, deviceActivity.DeviceType.String)
}

func toRequestedFrom(deviceActivity *model.SessionActivity) string {
	deviceInfo := []string{
		deviceActivity.IPAddress.String,
		deviceActivity.City.String,
		deviceActivity.Country.String,
	}

	deviceInfo = clerkstrings.Compact(deviceInfo)

	if len(deviceInfo) > 0 {
		return strings.Join(deviceInfo, ", ")
	}

	return "unknown location"
}

const requestedAtFormat = "02 January 2006, 15:04 MST"

func toRequestedAt(clock clockwork.Clock) string {
	return clock.Now().Format(requestedAtFormat)
}
