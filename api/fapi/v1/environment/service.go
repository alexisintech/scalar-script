package environment

import (
	"context"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/environment"
	"clerk/api/shared/sentryenv"
	"clerk/api/shared/sso"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	ctxenv "clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requestdomain"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/oauth/provider"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/log"
	"clerk/utils/url"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	db database.Database

	// services
	environmentService *environment.Service

	// repositories
	applicationOwnershipRepo *repository.ApplicationOwnerships
	devBrowserRepo           *repository.DevBrowser
	imageRepo                *repository.Images
}

func NewService(db database.Database) *Service {
	return &Service{
		db:                       db,
		environmentService:       environment.NewService(),
		applicationOwnershipRepo: repository.NewApplicationOwnerships(),
		devBrowserRepo:           repository.NewDevBrowser(),
		imageRepo:                repository.NewImages(),
	}
}

// SetEnvFromDomain sets the environment on the context based on the domain of the request
func (s *Service) SetEnvFromDomain(ctx context.Context) (context.Context, apierror.Error) {
	domain := requestdomain.FromContext(ctx)

	env, err := s.environmentService.LoadByDomain(ctx, s.db, domain)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	}

	log.AddToLogLine(ctx, log.InstanceID, env.Instance.ID)
	log.AddToLogLine(ctx, log.EnvironmentType, env.Instance.EnvironmentType)
	log.AddToLogLine(ctx, log.DomainName, env.Domain.Name)

	sentryenv.EnrichScope(ctx, env)
	return ctxenv.NewContext(ctx, env), nil
}

// Read returns the environment of an instance
func (s *Service) Read(ctx context.Context) (*serialize.EnvironmentResponse, apierror.Error) {
	env := ctxenv.FromContext(ctx)
	devBrowser := requestingdevbrowser.FromContext(ctx)

	images, err := s.imageRepo.AppImages(ctx, s.db, env.Application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	err = s.populateApplicationDemoStatus(ctx, env.Application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	googleOneTapClientID, err := s.fetchGoogleOneTapClientID(ctx, env.AuthConfig)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Environment(ctx, env, images, devBrowser, googleOneTapClientID), nil
}

// Update updates the environment as necessary
func (s *Service) Update(ctx context.Context, origin string) (*serialize.EnvironmentResponse, apierror.Error) {
	env := ctxenv.FromContext(ctx)

	if env.Instance.IsProduction() {
		return nil, apierror.InvalidRequestForEnvironment(string(constants.ETDevelopment), string(constants.ETStaging))
	}

	err := s.populateApplicationDemoStatus(ctx, env.Application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	images, err := s.imageRepo.AppImages(ctx, s.db, env.Application)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	googleOneTapClientID, err := s.fetchGoogleOneTapClientID(ctx, env.AuthConfig)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	devBrowser := requestingdevbrowser.FromContext(ctx)
	if devBrowser == nil {
		return serialize.Environment(ctx, env, images, devBrowser, googleOneTapClientID), nil
	}

	isDevAccountsOrigin, err := isDevAccountsOrigin(origin)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if !isDevAccountsOrigin {
		devBrowser.HomeOrigin = null.StringFrom(origin)
		if err = s.devBrowserRepo.UpdateHomeOrigin(ctx, s.db, devBrowser); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	return serialize.Environment(ctx, env, images, devBrowser, googleOneTapClientID), nil
}

func isDevAccountsOrigin(origin string) (bool, error) {
	urlInfo, err := url.Analyze(origin)
	if err != nil {
		return false, err
	}

	// accounts.foo.bar-13.lcl.dev OR accounts.foo.bar-13.lclstage.dev OR accounts.foo.bar-13.dev.lclclerk.com
	isLegacyAccounts := strings.HasPrefix(urlInfo.Host, "accounts.") && (strings.HasSuffix(urlInfo.Host, cenv.Get(cenv.ClerkDevDomainSuffix)) || strings.HasSuffix(urlInfo.Host, cenv.Get(cenv.ClerkStgDomainSuffix)))
	// foo-bar-13.accounts.dev OR foo-bar-13.accountstage.dev OR foo-bar-13.accounts.lclclerk.com
	isKimaAccounts := strings.HasSuffix(urlInfo.Host, cenv.Get(cenv.ClerkPublishableKeySharedDevDomain)) && !strings.HasSuffix(urlInfo.Host, ".clerk."+cenv.Get(cenv.ClerkPublishableKeySharedDevDomain))

	return isLegacyAccounts || isKimaAccounts, nil
}

func (s *Service) populateApplicationDemoStatus(ctx context.Context, app *model.Application) error {
	hasOwner, err := s.applicationOwnershipRepo.ExistsAppOwner(ctx, s.db, app.ID)
	if err != nil {
		return err
	}

	app.Demo = !hasOwner

	return nil
}

func (s *Service) fetchGoogleOneTapClientID(ctx context.Context, authConfig *model.AuthConfig) (*string, error) {
	userSettings := usersettings.NewUserSettings(authConfig.UserSettings)
	if !cenv.ResourceHasAccess(cenv.FlagAllowGoogleOneTapInstanceIDs, authConfig.InstanceID) || !userSettings.GoogleOneTapEnabled() {
		return nil, nil
	}

	oauthConfig, err := sso.ActiveOauthConfigForProvider(ctx, s.db, authConfig.ID, provider.GoogleID())
	if err != nil {
		return nil, err
	}

	return &oauthConfig.ClientID, nil
}
