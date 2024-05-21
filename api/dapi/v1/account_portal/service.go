package account_portal

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/dapi/serialize"
	dashboardDomains "clerk/api/dapi/v1/domains"
	"clerk/api/shared/domains"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/validator"
	clerkjson "clerk/pkg/json"
	"clerk/pkg/sdk"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	db        database.Database
	gueClient *gue.Client

	// services
	sharedDomainService    *domains.Service
	dashboardDomainService *dashboardDomains.Service

	// repositories
	accountPortalRepo *repository.AccountPortal
	displayConfigRepo *repository.DisplayConfig
	domainRepo        *repository.Domain
}

func NewService(deps clerk.Deps, sdkConfigConstructor sdk.ConfigConstructor) *Service {
	return &Service{
		db:                     deps.DB(),
		gueClient:              deps.GueClient(),
		accountPortalRepo:      repository.NewAccountPortal(),
		displayConfigRepo:      repository.NewDisplayConfig(),
		sharedDomainService:    domains.NewService(deps),
		domainRepo:             repository.NewDomain(),
		dashboardDomainService: dashboardDomains.NewService(deps, sdkConfigConstructor),
	}
}

type updateParams struct {
	Enabled                     *bool            `json:"enabled"`
	AfterSignInPath             clerkjson.String `json:"after_sign_in_path" validate:"omitempty,startswith=/|startswith=?|startswith=#|eq="`
	AfterSignUpPath             clerkjson.String `json:"after_sign_up_path" validate:"omitempty,startswith=/|startswith=?|startswith=#|eq="`
	AfterCreateOrganizationPath clerkjson.String `json:"after_create_organization_path" validate:"omitempty,startswith=/|startswith=?|startswith=#|eq="`
	AfterLeaveOrganizationPath  clerkjson.String `json:"after_leave_organization_path" validate:"omitempty,startswith=/|startswith=?|startswith=#|eq="`
	LogoLinkPath                clerkjson.String `json:"logo_link_path" validate:"omitempty,startswith=/|startswith=?|startswith=#|eq="`
}

// Read returns the account_portal for the given instance
func (s *Service) Read(ctx context.Context, instanceID string) (*serialize.AccountPortalResponse, apierror.Error) {
	accountPortal, err := s.accountPortalRepo.QueryByInstanceID(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if accountPortal == nil {
		return nil, apierror.ResourceNotFound()
	}

	return serialize.AccountPortal(accountPortal), nil
}

// Update updates the account_portal with the given values
func (s *Service) Update(ctx context.Context, instanceID string, params *updateParams) (*serialize.AccountPortalResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	accountPortal, err := s.accountPortalRepo.QueryByInstanceID(ctx, s.db, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if accountPortal == nil {
		return nil, apierror.ResourceNotFound()
	}

	validate := validator.FromContext(ctx)

	var formErrs apierror.Error

	if err = validate.Struct(params); err != nil {
		formErrs = apierror.Combine(formErrs, apierror.FormValidationFailed(err))
	}

	if formErrs != nil {
		return nil, formErrs
	}

	whitelistColumns := set.New[string]()

	if params.Enabled != nil {
		accountPortal.Enabled = *params.Enabled
		whitelistColumns.Insert(sqbmodel.AccountPortalColumns.Enabled)
	}

	if params.AfterSignInPath.IsSet {
		accountPortal.Paths.AfterSignIn = null.StringFromPtr(params.AfterSignInPath.Ptr())
		whitelistColumns.Insert(sqbmodel.AccountPortalColumns.Paths)
	}

	if params.AfterSignUpPath.IsSet {
		accountPortal.Paths.AfterSignUp = null.StringFromPtr(params.AfterSignUpPath.Ptr())
		whitelistColumns.Insert(sqbmodel.AccountPortalColumns.Paths)
	}

	if params.AfterCreateOrganizationPath.IsSet {
		accountPortal.Paths.AfterCreateOrganization = null.StringFromPtr(params.AfterCreateOrganizationPath.Ptr())
		whitelistColumns.Insert(sqbmodel.AccountPortalColumns.Paths)
	}

	if params.AfterLeaveOrganizationPath.IsSet {
		accountPortal.Paths.AfterLeaveOrganization = null.StringFromPtr(params.AfterLeaveOrganizationPath.Ptr())
		whitelistColumns.Insert(sqbmodel.AccountPortalColumns.Paths)
	}

	if params.LogoLinkPath.IsSet {
		accountPortal.Paths.LogoLink = null.StringFromPtr(params.LogoLinkPath.Ptr())
		whitelistColumns.Insert(sqbmodel.AccountPortalColumns.Paths)
	}

	var (
		triggerDNSChecks bool
		primaryDomainID  string
	)
	if whitelistColumns.Count() > 0 {
		txErr := s.db.PerformTxWithEmitter(ctx, s.gueClient, func(txEmitter database.TxEmitter) (bool, error) {
			err := s.accountPortalRepo.Update(ctx, txEmitter, accountPortal, whitelistColumns.Array()...)
			if err != nil {
				return true, err
			}

			// Also sync path changes to the display_config for the case that we need to switch the Account Portal feature off
			if whitelistColumns.Contains(sqbmodel.AccountPortalColumns.Paths) {
				env.DisplayConfig.Paths.AfterSignIn = accountPortal.Paths.AfterSignIn
				env.DisplayConfig.Paths.AfterSignUp = accountPortal.Paths.AfterSignUp
				env.DisplayConfig.Paths.AfterCreateOrganization = accountPortal.Paths.AfterCreateOrganization
				env.DisplayConfig.Paths.AfterLeaveOrganization = accountPortal.Paths.AfterLeaveOrganization

				// logo_link_url is not exposed on the display config settings

				err = s.displayConfigRepo.UpdatePaths(ctx, txEmitter, env.DisplayConfig)
				if err != nil {
					return true, err
				}
			}

			// Update the domain so that we don't include the accounts sub-domain in DNS CNAME requirements,
			// if the Account Portal is disabled.
			if whitelistColumns.Contains(sqbmodel.AccountPortalColumns.Enabled) {
				if err := s.togglePrimaryDomainDNSRequirements(ctx, txEmitter, env.Instance, accountPortal.Enabled); err != nil {
					return true, err
				}
				triggerDNSChecks = accountPortal.Enabled && env.Instance.IsProduction()
				primaryDomainID = env.Instance.ActiveDomainID
			}

			return false, nil
		})
		if txErr != nil {
			return nil, apierror.Unexpected(txErr)
		}
	}

	// We need to retry the DNS checks outside of the transaction in order to avoid dead-locking on the DNS check record.
	if triggerDNSChecks {
		if err := s.dashboardDomainService.RetryDNS(ctx, instanceID, primaryDomainID); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	return serialize.AccountPortal(accountPortal), nil
}

func (s *Service) togglePrimaryDomainDNSRequirements(ctx context.Context, exec database.Executor, instance *model.Instance, enabled bool) error {
	primaryDomain, err := s.domainRepo.QueryByID(ctx, exec, instance.ActiveDomainID)
	if err != nil {
		return err
	}
	if primaryDomain == nil {
		return nil
	}
	// If a proxy URL has been set while the Account Portal was disabled, then the CNAME requirement
	// will be absent. Once the customer enables the account portal, the requirement must be reinstated.
	if primaryDomain.ProxyURL.Valid && enabled && !primaryDomain.HasDisabledAccounts {
		return nil
	}
	if primaryDomain.ProxyURL.Valid && !enabled {
		return nil
	}
	primaryDomain.HasDisabledAccounts = !enabled
	if err := s.domainRepo.UpdateHasDisabledAccounts(ctx, exec, primaryDomain); err != nil {
		return err
	}
	return s.sharedDomainService.RefreshCNAMERequirements(ctx, exec, instance, primaryDomain)
}
