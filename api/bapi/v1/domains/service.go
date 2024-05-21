package domains

import (
	"context"
	"strings"

	"clerk/api/apierror"
	"clerk/api/bapi/v1/externalapp"
	"clerk/api/bapi/v1/internalapi"
	proxyChecks "clerk/api/bapi/v1/proxy_checks"
	"clerk/api/serialize"
	"clerk/api/shared/domains"
	"clerk/api/shared/edgecache"
	"clerk/api/shared/edgereplication"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/clerkvalidator"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/generate"
	"clerk/pkg/jobs"
	clerkjson "clerk/pkg/json"
	"clerk/pkg/set"
	"clerk/pkg/validators"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/url"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock     clockwork.Clock
	db        database.Database
	gueClient *gue.Client

	// validators
	validator *validator.Validate

	// repositories
	dnsCheckRepo         *repository.DNSChecks
	domainRepo           *repository.Domain
	instanceRepo         *repository.Instances
	proxyCheckRepo       *repository.ProxyCheck
	subscriptionPlanRepo *repository.SubscriptionPlans

	// services
	proxyCheckService      *proxyChecks.Service
	sharedDomainService    *domains.Service
	edgeReplicationService *edgereplication.Service
}

func NewService(
	deps clerk.Deps,
	externalAppClient *externalapp.Client,
	internalClient *internalapi.Client,
) *Service {
	return &Service{
		clock:                  deps.Clock(),
		db:                     deps.DB(),
		gueClient:              deps.GueClient(),
		validator:              clerkvalidator.New(),
		dnsCheckRepo:           repository.NewDNSChecks(),
		domainRepo:             repository.NewDomain(),
		instanceRepo:           repository.NewInstances(),
		proxyCheckRepo:         repository.NewProxyCheck(),
		subscriptionPlanRepo:   repository.NewSubscriptionPlans(),
		proxyCheckService:      proxyChecks.NewService(deps.Clock(), deps.DB(), deps.GueClient(), externalAppClient, internalClient),
		sharedDomainService:    domains.NewService(deps),
		edgeReplicationService: edgereplication.NewService(deps.GueClient(), cenv.GetBool(cenv.FlagReplicateInstanceToEdgeJobsEnabled)),
	}
}

type CreateParams struct {
	Name        string         `json:"name" form:"name"`
	IsSatellite clerkjson.Bool `json:"is_satellite" form:"is_satellite"`
	ProxyURL    *string        `json:"proxy_url,omitempty" form:"proxy_url,omitempty"`

	// Result of domain name URL analysis
	nameURLInfo *url.Info

	// Required to validate the proxy url if present
	authorization string
}

func (params *CreateParams) normalize() apierror.Error {
	params.Name = strings.ToLower(params.Name)

	urlInfo, err := url.Analyze("https://" + params.Name)
	if err != nil {
		return apierror.FormInvalidParameterValue("name", params.Name)
	}
	params.nameURLInfo = urlInfo

	return nil
}

func (params *CreateParams) validate(validator *validator.Validate, isDevelopment bool) apierror.Error {
	var validationErr apierror.Error

	if params.ProxyURL != nil {
		validationErr = apierror.Combine(
			validationErr,
			validators.ProxyURL(validators.ProxyURLInput{
				ProxyURL:      *params.ProxyURL,
				Origin:        "https://" + params.Name,
				IsDevelopment: isDevelopment,
				Validator:     validator,
			}),
		)
	}

	if !params.IsSatellite.IsSet {
		validationErr = apierror.Combine(validationErr, apierror.FormMissingParameter("is_satellite"))
	} else if !params.IsSatellite.Value {
		validationErr = apierror.Combine(validationErr, apierror.PrimaryDomainAlreadyExists())
	}

	validationErr = apierror.Combine(
		validationErr,
		validators.DomainName(validators.DomainNameInput{
			URLInfo:       params.nameURLInfo,
			IsDevelopment: isDevelopment,
			IsSatellite:   params.IsSatellite.Value,
			ProxyURL:      params.ProxyURL,
			ParamName:     "name",
			Validator:     validator,
		}),
	)

	return validationErr
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.DomainResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	instance := env.Instance

	if err := params.normalize(); err != nil {
		return nil, err
	}

	valErr := params.validate(s.validator, instance.IsDevelopmentOrStaging())
	if valErr != nil {
		return nil, valErr
	}

	if !env.Instance.HasAccessToAllFeatures() {
		plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		unsupportedFeatures := billing.ValidateSupportedFeatures(
			billing.MultiDomainFeatures(instance.CreatedAt),
			env.Subscription,
			plans...,
		)
		if len(unsupportedFeatures) > 0 {
			return nil, apierror.UnsupportedSubscriptionPlanFeatures(unsupportedFeatures)
		}
	}

	var dmn *model.Domain
	var err error
	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		domainParams := generate.DomainParams{
			Instance: instance,
		}
		if instance.IsProduction() {
			domainParams.Name = params.Name
		} else {
			domainParams.DevName = null.StringFrom(params.Name)
		}
		dmn, err = generate.Domain(ctx, txEmitter, domainParams)
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueDomainName) ||
			clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueDomainDevName) {
			return true, apierror.FormIdentifierExists("name")
		} else if err != nil {
			return true, err
		}

		dmn.ProxyURL = null.StringFromPtr(params.ProxyURL)
		dmn.HasDisabledAccounts = true
		err = s.domainRepo.Update(
			ctx,
			txEmitter,
			dmn,
			sqbmodel.DomainColumns.ProxyURL,
			sqbmodel.DomainColumns.HasDisabledAccounts,
		)
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueDomainProxyURL) {
			return true, apierror.FormIdentifierExists("proxy_url")
		} else if err != nil {
			return true, err
		}

		// Generate the necessary status checks for DNS (production instances) or proxy.
		if instance.IsProduction() {
			_, err = generate.DNSCheck(ctx, txEmitter, instance, dmn)
			if err != nil {
				return true, err
			}
		}
		if dmn.ProxyURL.Valid {
			err := s.scheduleProxyCheck(ctx, txEmitter, dmn)
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

	cnameTargets := toCNameTargets(env.Instance, dmn)
	return serialize.Domain(dmn, env.Instance, serialize.WithCNameTargets(cnameTargets)), nil
}

func (s *Service) List(ctx context.Context) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	domains, err := s.domainRepo.FindAllByInstanceID(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]any, len(domains))
	for i, domain := range domains {
		cnameTargets := toCNameTargets(env.Instance, domain)
		response[i] = serialize.Domain(domain, env.Instance, serialize.WithCNameTargets(cnameTargets))
	}
	return serialize.Paginated(response, int64(len(response))), nil
}

type UpdateParams struct {
	ProxyURL clerkjson.String `json:"proxy_url" form:"proxy_url"`
	Name     clerkjson.String `json:"name" form:"name"`

	// Holds information about the domain if the name is valid.
	nameURLInfo *url.Info

	// Required to validate the proxy url if present
	authorization string
}

func (params *UpdateParams) normalize() apierror.Error {
	if params.Name.Valid {
		params.Name = clerkjson.StringFrom(strings.ToLower(params.Name.Value))

		if params.Name.Value == "" {
			return apierror.FormMissingParameter("name")
		}
		urlInfo, err := url.Analyze("https://" + params.Name.Value)
		if err != nil {
			return apierror.FormInvalidParameterValue("name", params.Name.Value)
		}
		params.nameURLInfo = urlInfo
	}
	return nil
}

func (params *UpdateParams) validate(
	domain *model.Domain,
	validator *validator.Validate,
	isProduction,
	isPrimary bool,
) apierror.Error {
	var validationErr apierror.Error

	if params.Name.IsSet {
		// Domain name must match the proxy URL. See if there's a
		// proxy URL set on the domain, or in the parameters.
		proxyURL := domain.ProxyURL.Ptr()
		if params.ProxyURL.IsSet {
			proxyURL = params.ProxyURL.Ptr()
		}
		validationErr = apierror.Combine(
			validationErr,
			validators.DomainName(validators.DomainNameInput{
				URLInfo:       params.nameURLInfo,
				IsDevelopment: !isProduction,
				IsSatellite:   !isPrimary,
				ProxyURL:      proxyURL,
				ParamName:     "name",
				Validator:     validator,
			}),
		)
	}

	if params.ProxyURL.IsSet {
		err := params.validateProxyURL(domain, validator, isProduction)
		if err != nil {
			validationErr = apierror.Combine(validationErr, err)
		}
	}

	return validationErr
}

func (params *UpdateParams) validateProxyURL(
	domain *model.Domain,
	validator *validator.Validate,
	isProduction bool,
) apierror.Error {
	// Determine the domain name, either from the dev_name for
	// development instances, or from the parameters.
	domainName := domain.Name
	if !isProduction && domain.DevName.Valid {
		domainName = domain.DevName.String
	}
	if params.Name.IsSet {
		domainName = params.Name.Value
	}

	// If proxy url is blank, it means the domain will stop operating with
	// a proxy.
	if !params.ProxyURL.Valid || params.ProxyURL.Value == "" {
		// Make sure that the domain is not on a subdomain, because that would
		// make it non-operational in production.
		if isProduction {
			// Ignore any error, there's probably something wrong with the domain name.
			domainURLInfo, err := url.Analyze("https://" + domainName)
			if err == nil && domainURLInfo.Subdomain != "" {
				return apierror.FormInvalidParameterFormat("proxy_url", "cannot be blank for non eTLD+1 domain")
			}
		}
		return nil
	}

	return validators.ProxyURL(validators.ProxyURLInput{
		ProxyURL:      params.ProxyURL.Value,
		Origin:        "https://" + domainName,
		IsDevelopment: !isProduction,
		Validator:     validator,
	})
}

func (s *Service) Update(ctx context.Context, domainID string, params UpdateParams) (*serialize.DomainResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	instance := env.Instance
	isProduction := instance.IsProduction()

	domain, err := s.domainRepo.QueryByIDAndInstanceID(ctx, s.db, domainID, instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if domain == nil {
		return nil, apierror.ResourceNotFound()
	}

	if err := params.normalize(); err != nil {
		return nil, err
	}

	isPrimary := domain.IsPrimary(instance)
	validationErr := params.validate(domain, s.validator, isProduction, isPrimary)
	if validationErr != nil {
		return nil, validationErr
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		dmnCols := set.New[string]()

		// NOTE: schedule the purge jobs before domain.Name is updated below,
		// because we want to bust the cache of the original domain name (i.e.
		// prior to the change) and hence we want the cache tags computed
		// based on the original domain name value.
		err = edgecache.PurgeJWKS(ctx, s.gueClient, tx, domain)
		if err != nil {
			return true, err
		}

		if params.ProxyURL.IsSet {
			if !params.ProxyURL.Valid || params.ProxyURL.Value == "" {
				domain.ProxyURL = null.StringFromPtr(nil)
			} else {
				domain.ProxyURL = null.StringFrom(params.ProxyURL.Value)
				err := s.scheduleProxyCheck(ctx, tx, domain)
				if err != nil {
					return true, err
				}
			}
			dmnCols.Insert(sqbmodel.DomainColumns.ProxyURL)
		}

		if params.Name.IsSet {
			if !isProduction {
				domain.DevName = null.StringFrom(params.Name.Value)
				dmnCols.Insert(sqbmodel.DomainColumns.DevName)
			} else if isPrimary {
				instance.HomeOrigin = null.StringFrom(params.nameURLInfo.Origin)
				if err := s.instanceRepo.UpdateHomeOrigin(ctx, tx, instance); err != nil {
					return true, err
				}

				// Only update if domain has changed.
				// We don't need to do anything if only the subdomain was updated.
				if domain.Name != params.nameURLInfo.Domain {
					domain.Name = params.nameURLInfo.Domain
					dmnCols.Insert(sqbmodel.DomainColumns.Name)

					// This also deletes any dns checks.
					err := s.sharedDomainService.Reset(ctx, tx, domain)
					if err != nil {
						return true, err
					}

					_, err = generate.DNSCheck(ctx, tx, instance, domain)
					if err != nil {
						return true, err
					}
				}
			} else { // Satellite domain
				domain.Name = params.Name.Value
				dmnCols.Insert(sqbmodel.DomainColumns.Name)

				err := s.sharedDomainService.Reset(ctx, tx, domain)
				if err != nil {
					return true, err
				}

				_, err = generate.DNSCheck(ctx, tx, instance, domain)
				if err != nil {
					return true, err
				}
			}
		}

		if dmnCols.Count() == 0 {
			return false, nil
		}

		err = s.domainRepo.Update(ctx, tx, domain, dmnCols.Array()...)
		if err != nil {
			return true, err
		}

		err = s.sharedDomainService.RefreshCNAMERequirements(ctx, tx, instance, domain)
		if err != nil {
			return true, err
		}

		// Enqueue a Gue job to replicate the domain to the edge data store.
		// We don't expect this to fail as it only adds the job in Postgres
		// and on database error we cannot commit either way.
		if err = s.edgeReplicationService.EnqueuePutDomain(ctx, tx, domainID); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueDomainName) ||
			clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueDomainDevName) {
			return nil, apierror.FormIdentifierExists("name")
		}

		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueDomainProxyURL) {
			return nil, apierror.FormIdentifierExists("proxy_url")
		}

		return nil, apierror.Unexpected(txErr)
	}

	cnameTargets := toCNameTargets(instance, domain)
	return serialize.Domain(domain, instance, serialize.WithCNameTargets(cnameTargets)), nil
}

func toCNameTargets(instance *model.Instance, domain *model.Domain) []serialize.CNameTarget {
	cnameRequirements := model.CNAMERequirements(instance, domain)
	cnameTargets := make([]serialize.CNameTarget, len(cnameRequirements))
	for i, cname := range cnameRequirements {
		cnameTargets[i] = serialize.CNameTarget{
			Host:     cname.Label.FqdnSource(domain),
			Value:    cname.Label.FqdnTarget(domain),
			Required: !cname.Optional,
		}
	}
	return cnameTargets
}

func (s *Service) Delete(ctx context.Context, domainID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	domain, err := s.domainRepo.QueryByIDAndInstanceID(ctx, s.db, domainID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if domain == nil {
		return nil, apierror.ResourceNotFound()
	}

	if !domain.IsSatellite(env.Instance) {
		return nil, apierror.OperationNotAllowedOnPrimaryDomain()
	}

	txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
		err := s.sharedDomainService.Delete(ctx, txEmitter, domain)
		return err != nil, err
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.DeletedObject(domainID, serialize.ObjectDomain), nil
}

func (s *Service) scheduleProxyCheck(
	ctx context.Context,
	tx database.Tx,
	domain *model.Domain,
) error {
	proxyCheck, err := s.proxyCheckService.FindOrCreateByDomainAndProxyURL(ctx, tx, domain, domain.ProxyURL.String)
	if err != nil {
		return err
	}
	return jobs.CheckProxy(ctx, s.gueClient, jobs.CheckProxyArgs{ProxyCheckID: proxyCheck.ID}, jobs.WithTx(tx))
}
