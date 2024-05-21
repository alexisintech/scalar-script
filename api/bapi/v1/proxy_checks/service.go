package proxy_checks

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/bapi/v1/externalapp"
	"clerk/api/bapi/v1/internalapi"
	"clerk/api/serialize"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkvalidator"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/jobs"
	"clerk/pkg/sentry"
	"clerk/pkg/validators"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock     clockwork.Clock
	db        database.Database
	validator *validator.Validate

	externalAppClient *externalapp.Client
	gueClient         *gue.Client
	internalClient    *internalapi.Client

	domainRepo     *repository.Domain
	proxyCheckRepo *repository.ProxyCheck
}

func NewService(
	clock clockwork.Clock,
	db database.Database,
	gueClient *gue.Client,
	externalAppClient *externalapp.Client,
	internalClient *internalapi.Client,
) *Service {
	return &Service{
		clock:             clock,
		db:                db,
		validator:         clerkvalidator.New(),
		externalAppClient: externalAppClient,
		gueClient:         gueClient,
		internalClient:    internalClient,
		domainRepo:        repository.NewDomain(),
		proxyCheckRepo:    repository.NewProxyCheck(),
	}
}

// FindOrCreateByDomainAndProxyURL returns the first proxy check for the
// provided domain and proxyURL combination. A proxy check will be created
// if none exists.
// FindOrCreateByDomainAndProxyURL will lock the domain record so that
// any associations which have a foreign key pointing to the domain's ID
// will be locked as well.
func (s *Service) FindOrCreateByDomainAndProxyURL(
	ctx context.Context,
	tx database.Tx,
	dmn *model.Domain,
	proxyURL string,
) (*model.ProxyCheck, error) {
	// Lock the domain record so that we can get a lock on the
	// proxy checks table through the FK association.
	if _, err := s.domainRepo.FindByIDForUpdate(ctx, tx, dmn.ID); err != nil {
		return nil, err
	}

	proxyCheck, err := s.proxyCheckRepo.QueryByDomainIDProxyURL(ctx, tx, dmn.ID, proxyURL)
	if err != nil {
		return nil, err
	}
	if proxyCheck != nil {
		return proxyCheck, nil
	}

	proxyCheck = model.NewProxyCheck(dmn)
	proxyCheck.ProxyURL = proxyURL
	return s.proxyCheckRepo.Insert(ctx, tx, proxyCheck)
}

type createParams struct {
	DomainID string `json:"domain_id" form:"domain_id" validate:"required"`
	ProxyURL string `json:"proxy_url" form:"proxy_url" validate:"required"`
	// Authorization header that is used for authenticating with BAPI. Contains the
	// full Bearer <secret-key> header value.
	authorization string `json:"-"`
}

func (p *createParams) validate(validator *validator.Validate, isDevelopment bool, origin string) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return validators.ProxyURL(validators.ProxyURLInput{
		ProxyURL:      p.ProxyURL,
		IsDevelopment: isDevelopment,
		Origin:        origin,
		Validator:     validator,
	})
}

// Create finds or inserts a proxy check record for the given
// domain ID and proxy URL pair. It runs a real-time health check
// to validate that the FAPI is accessible through the proxy URL.
// A proper error will be returned for unsuccessful health checks.
// Successful health checks lead to responses returning a serialized
// version of the proxy check record.
// The proxy check record will be created or updated with the health
// check's result no matter the outcome.
func (s *Service) Create(ctx context.Context, params createParams) (*serialize.ProxyCheckResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	instance := env.Instance

	domain, err := s.domainRepo.QueryByIDAndInstanceID(ctx, s.db, params.DomainID, instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if domain == nil {
		return nil, apierror.ResourceNotFound()
	}

	valErr := params.validate(s.validator, instance.IsDevelopment(), "https://"+domain.Name)
	if valErr != nil {
		return nil, valErr
	}

	var proxyCheck *model.ProxyCheck
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		proxyCheck, err = s.FindOrCreateByDomainAndProxyURL(ctx, tx, domain, params.ProxyURL)
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	apiErr := s.validateProxyURLHealth(ctx, validateProxyURLHealthParams{
		proxyCheck:    proxyCheck,
		domainID:      domain.ID,
		authorization: params.authorization,
	})
	if apiErr != nil {
		return nil, apiErr
	}
	return serialize.ProxyCheck(proxyCheck), nil
}

type validateProxyURLHealthParams struct {
	proxyCheck    *model.ProxyCheck
	domainID      string
	authorization string
}

// Returns an error if the proxy setup on the provided proxy URL
// in the params is not valid.
// A proxy configuration is valid if:
// 1. FAPI healthcheck can be reached via the proxy URL
// 2. The x_forwarded_for response field matches our IP address
// The function will update the proxy check record from the params
// according to the health check result.
func (s *Service) validateProxyURLHealth(ctx context.Context, params validateProxyURLHealthParams) apierror.Error {
	proxyCheck := params.proxyCheck

	// XXX We'll mark the proxy check as successful without performing the actual check if
	// the server is configured to skip proxy checks.
	if cenv.IsEnabled(cenv.SkipProxyConfigChecks) {
		proxyCheck.Successful = true
		err := s.proxyCheckRepo.UpdateSuccessful(ctx, s.db, proxyCheck)
		if err != nil {
			return apierror.Unexpected(err)
		}
		return nil
	}

	// A valid proxy configuration needs to include the original client's IP
	// address in the X-Forwarded-For header.
	whatIsMyIP, err := s.internalClient.WhatIsMyIP(ctx, params.authorization)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if whatIsMyIP.IP == "" || len(whatIsMyIP.Errors) > 0 {
		msg := "cannot determine IP address"
		if len(whatIsMyIP.Errors) > 0 {
			msg += ": " + whatIsMyIP.Errors[0].Message
		}
		return apierror.Unexpected(fmt.Errorf(msg))
	}

	// This is a new validation attempt. Let's reset the state on the proxy check.
	// In case an error happens before the health check completes, the value will
	// be written in the deferred function below.
	proxyCheck.Successful = false

	// Check the proxy status via FAPI.
	// Note that the request lives outside the transaction because we want to
	// make sure that the proxy check record is already persisted when the
	// request reaches FAPI.
	res, err := s.externalAppClient.GetProxyHealth(ctx, &externalapp.ProxyHealthParams{
		ProxyURL:      proxyCheck.ProxyURL,
		DomainID:      params.domainID,
		XForwardedFor: whatIsMyIP.IP,
	})
	if err != nil {
		return apierror.InvalidProxyConfiguration("Failed to validate proxy configuration.")
	}

	proxyCheck.LastRawResult = null.StringFrom(string(res.Raw))
	proxyCheck.LastRunAt = null.TimeFrom(s.clock.Now().UTC())

	// No matter what happens, we will update the proxy check
	// record with the results
	defer func() {
		err := s.proxyCheckRepo.UpdateLastRawResultLastRunAtSuccessful(ctx, s.db, proxyCheck)
		if err != nil {
			sentry.CaptureException(ctx, err)
		}
	}()
	if !res.Success() {
		return apierror.InvalidProxyConfiguration(res.Message)
	}

	// XXX(gkats) Temporarily skip x-forwarded-for checks. We noticed that they're
	// not working as expected in production and the fix may take a while.
	if !cenv.GetBool(cenv.SkipProxyXForwardedForChecks) {
		// As a last step, we have to validate that the response's XForwardedFor
		// value matches our IP address.
		if res.XForwardedFor != whatIsMyIP.IP {
			return apierror.InvalidProxyConfiguration("X-Forwarded-For header was not set correctly.")
		}
	}

	// If we're here, it means that the proxy health check succeeded.
	proxyCheck.Successful = true

	if err := jobs.TrackInstanceLive(ctx, s.gueClient, jobs.TrackInstanceLiveArgs{DomainID: params.domainID}); err != nil {
		sentry.CaptureException(ctx, err)
	}
	return nil
}
