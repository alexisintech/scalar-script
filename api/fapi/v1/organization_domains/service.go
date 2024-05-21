package organization_domains

import (
	"context"
	"errors"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/emailquality"
	"clerk/api/shared/events"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/api/shared/serializable"
	"clerk/api/shared/strategies"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/emailaddress"
	"clerk/pkg/organizationsettings"
	"clerk/pkg/psl"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/volatiletech/null/v8"
)

const (
	maxDomainsPerOrganization = 10
)

type Service struct {
	deps clerk.Deps
	db   database.Database

	// services
	eventService         *events.Service
	organizationsService *organizations.Service
	serializableService  *serializable.Service
	emailQualityService  *emailquality.EmailQuality

	// repositories
	identificationRepo                 *repository.Identification
	organizationDomainRepo             *repository.OrganizationDomain
	organizationDomainVerificationRepo *repository.OrganizationDomainVerification
	organizationInvitationRepo         *repository.OrganizationInvitation
	organizationSuggestionRepo         *repository.OrganizationSuggestion
	organizationRepo                   *repository.Organization
	emailDomainReportsRepo             *repository.EmailDomainReport
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		deps:                               deps,
		db:                                 deps.DB(),
		eventService:                       events.NewService(deps),
		organizationsService:               organizations.NewService(deps),
		serializableService:                serializable.NewService(deps.Clock()),
		emailQualityService:                deps.EmailQualityChecker(),
		identificationRepo:                 repository.NewIdentification(),
		organizationDomainRepo:             repository.NewOrganizationDomain(),
		organizationDomainVerificationRepo: repository.NewOrganizationDomainVerification(),
		organizationInvitationRepo:         repository.NewOrganizationInvitation(),
		organizationSuggestionRepo:         repository.NewOrganizationSuggestion(),
		organizationRepo:                   repository.NewOrganization(),
		emailDomainReportsRepo:             repository.NewEmailDomainReport(),
	}
}

type CreateParams struct {
	Name           string
	OrganizationID string
}

func (params *CreateParams) validate() apierror.Error {
	// Sanitize params
	params.Name = strings.ToLower(params.Name)

	if ok := validate.DomainName(params.Name); !ok {
		return apierror.FormInvalidParameterFormat(param.OrgDomainName.Name, "Must be a valid domain name")
	}

	// Make sure the domain name is minimum eTLD+1
	eTLDPlusOne, err := psl.Domain(params.Name)
	if err != nil {
		if errors.Is(err, psl.ErrDomainIsSuffix) {
			// name param is a public suffix like `co.uk`
			return apierror.FormInvalidParameterFormat(param.OrgDomainName.Name, "Domain name must be at least eTLD+1")
		}
		return apierror.Unexpected(err)
	}
	if !strings.HasSuffix(params.Name, eTLDPlusOne) {
		return apierror.FormInvalidParameterFormat(param.OrgDomainName.Name, "Domain name must be at least eTLD+1")
	}

	return nil
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.OrganizationDomainResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionDomainsManage, user.ID); apiErr != nil {
		return nil, apiErr
	}

	// normalize domain name
	params.Name = strings.ToLower(params.Name)
	if apiErr := params.validate(); apiErr != nil {
		return nil, apiErr
	}

	// Make sure the domain is not a common or disposable email provider domain
	result, err := s.emailQualityService.CheckDomain(ctx, params.Name)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if result.Common {
		return nil, apierror.OrganizationDomainCommon(param.OrgDomainName.Name)
	}
	if result.Disposable {
		return nil, apierror.OrganizationDomainBlocked(param.OrgDomainName.Name)
	}

	totalDomains, err := s.organizationDomainRepo.CountByOrganization(ctx, s.db, params.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if totalDomains == maxDomainsPerOrganization {
		return nil, apierror.OrganizationDomainQuotaExceeded(maxDomainsPerOrganization)
	}

	var response *serialize.OrganizationDomainResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// do not allow org domain creation if a verified domain already exists for this instance with the same domain nane
		// multiple non-verified domains with the same name are ok
		orgDomainExisting, err := s.organizationDomainRepo.QueryVerifiedByInstanceAndName(ctx, tx, env.Instance.ID, params.Name)
		if err != nil {
			return false, err
		}
		if orgDomainExisting != nil {
			return false, apierror.OrganizationDomainAlreadyExists(param.OrgDomainName.Name)
		}

		organizationDomain := &model.OrganizationDomain{
			OrganizationDomain: &sqbmodel.OrganizationDomain{
				InstanceID:     env.Instance.ID,
				OrganizationID: params.OrganizationID,
				Name:           params.Name,
				EnrollmentMode: constants.EnrollmentModeManualInvitation,
				Verified:       false,
			},
		}
		err = s.organizationDomainRepo.Insert(ctx, tx, organizationDomain)
		if err != nil {
			return true, err
		}

		if err = s.autoVerifyIfPossible(ctx, tx, organizationDomain, user); err != nil {
			return true, err
		}

		orgDomainSerializable, err := s.serializableService.ConvertOrganizationDomain(ctx, tx, organizationDomain)
		if err != nil {
			return true, err
		}

		response = serialize.OrganizationDomain(orgDomainSerializable)

		if err := s.eventService.OrganizationDomainCreated(ctx, tx, env.Instance, response, params.OrganizationID); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueOrganizationDomainName) ||
			clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueVerifiedOrganizationDomainNameInstance) {
			return nil, apierror.OrganizationDomainAlreadyExists(param.OrgDomainName.Name)
		}
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

type ListOrganizationDomainsParams struct {
	OrganizationID  string
	Verified        *bool
	EnrollmentModes []string
}

func (params ListOrganizationDomainsParams) validate() apierror.Error {
	for _, enrollmentMode := range params.EnrollmentModes {
		if !constants.OrganizationDomainEnrollmentModes.Contains(enrollmentMode) {
			return apierror.FormInvalidParameterValueWithAllowed(param.OrgDomainEnrollmentMode.Name, enrollmentMode, constants.OrganizationDomainEnrollmentModes.Array())
		}
	}
	return nil
}

func (s *Service) ListOrganizationDomains(ctx context.Context, params ListOrganizationDomainsParams, paginationParams pagination.Params) (*serialize.PaginatedResponse, apierror.Error) {
	user := requesting_user.FromContext(ctx)

	if apiErr := params.validate(); apiErr != nil {
		return nil, apiErr
	}

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionDomainsRead, user.ID); apiErr != nil {
		return nil, apiErr
	}

	orgDomainMods := repository.OrganizationDomainsFindAllModifiers{
		Verified:        params.Verified,
		EnrollmentModes: params.EnrollmentModes,
	}

	domains, err := s.organizationDomainRepo.FindAllByOrganizationWithModifiers(
		ctx,
		s.db,
		params.OrganizationID,
		orgDomainMods,
		paginationParams,
	)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.organizationDomainRepo.CountByOrganizationWithModifiers(
		ctx,
		s.db,
		params.OrganizationID,
		orgDomainMods,
	)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]interface{}, len(domains))
	for i, domain := range domains {
		orgDomainSerializable, err := s.serializableService.ConvertOrganizationDomain(ctx, s.db, domain)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		response[i] = serialize.OrganizationDomain(orgDomainSerializable)
	}

	return serialize.Paginated(response, totalCount), nil
}

func (s *Service) Read(ctx context.Context, organizationID, domainID string) (*serialize.OrganizationDomainResponse, apierror.Error) {
	user := requesting_user.FromContext(ctx)

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, organizationID, constants.PermissionDomainsRead, user.ID); apiErr != nil {
		return nil, apiErr
	}

	orgDomain, err := s.organizationDomainRepo.QueryByIDAndOrganizationID(ctx, s.db, domainID, organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgDomain == nil {
		return nil, apierror.ResourceNotFound()
	}

	orgDomainSerializable, err := s.serializableService.ConvertOrganizationDomain(ctx, s.db, orgDomain)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.OrganizationDomain(orgDomainSerializable), nil
}

type PrepareParams struct {
	AffiliationEmailAddress string
	OrganizationID          string
	OrganizationDomainID    string
}

func (params *PrepareParams) sanitize() {
	params.AffiliationEmailAddress = strings.ToLower(params.AffiliationEmailAddress)
}

func (s *Service) PrepareAffiliationVerification(ctx context.Context, params PrepareParams) (*serialize.OrganizationDomainResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionDomainsManage, user.ID); apiErr != nil {
		return nil, apiErr
	}

	params.sanitize()

	orgDomain, err := s.organizationDomainRepo.QueryByIDAndOrganizationID(ctx, s.db, params.OrganizationDomainID, params.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgDomain == nil {
		return nil, apierror.ResourceNotFound()
	}

	// Make sure the email address matches the org domain
	if orgDomain.Name != emailaddress.Domain(params.AffiliationEmailAddress) {
		return nil, apierror.OrganizationDomainMismatch(param.AffiliationEmailAddress.Name)
	}

	var response *serialize.OrganizationDomainResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		preparer := strategies.NewAffiliationEmailCodePreparer(s.deps, env, orgDomain, params.AffiliationEmailAddress)
		verification, err := preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		orgDomain.VerificationID = null.StringFrom(verification.ID)
		orgDomain.AffiliationEmailAddress = null.StringFrom(params.AffiliationEmailAddress)
		if err = s.organizationDomainRepo.Update(ctx, tx, orgDomain, sqbmodel.OrganizationDomainColumns.VerificationID, sqbmodel.OrganizationDomainColumns.AffiliationEmailAddress); err != nil {
			return true, err
		}

		orgDomainSerializable, err := s.serializableService.ConvertOrganizationDomain(ctx, tx, orgDomain)
		if err != nil {
			return true, err
		}

		response = serialize.OrganizationDomain(orgDomainSerializable)

		if err := s.eventService.OrganizationDomainUpdated(ctx, tx, env.Instance, response, params.OrganizationID); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

type AttemptParams struct {
	Code                 string
	OrganizationID       string
	OrganizationDomainID string
}

func (s *Service) AttemptAffiliationVerification(ctx context.Context, params AttemptParams) (*serialize.OrganizationDomainResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionDomainsManage, user.ID); apiErr != nil {
		return nil, apiErr
	}

	orgDomain, err := s.organizationDomainRepo.QueryByIDAndOrganizationID(ctx, s.db, params.OrganizationDomainID, params.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgDomain == nil {
		return nil, apierror.ResourceNotFound()
	}

	verification, err := s.organizationDomainVerificationRepo.FindByID(ctx, s.db, orgDomain.VerificationID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if verification.Strategy != constants.VSAffiliationEmailCode {
		return nil, apierror.VerificationInvalidStrategy()
	}

	attemptor := strategies.NewAffiliationEmailCodeAttemptor(s.deps.Clock(), verification, orgDomain.ID, params.Code)

	var response *serialize.OrganizationDomainResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err = attemptor.Attempt(ctx, tx); err != nil {
			if errors.Is(err, strategies.ErrInvalidCode) {
				// No rollback in case of invalid code
				return false, err
			}
			return true, err
		}

		orgDomain.Verified = true
		if err = s.organizationDomainRepo.UpdateVerified(ctx, tx, orgDomain); err != nil {
			return true, err
		}

		// remove existing unverified domains with the same name
		err := s.organizationDomainRepo.DeleteUnverifiedByInstanceAndName(ctx, tx, env.Instance.ID, orgDomain.Name)
		if err != nil {
			return true, err
		}

		orgDomainSerializable, err := s.serializableService.ConvertOrganizationDomain(ctx, tx, orgDomain)
		if err != nil {
			return true, err
		}

		response = serialize.OrganizationDomain(orgDomainSerializable)

		if err := s.eventService.OrganizationDomainUpdated(ctx, tx, env.Instance, response, params.OrganizationID); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueVerifiedOrganizationDomainNameInstance) {
			return nil, apierror.OrganizationDomainAlreadyExists(param.Code.Name)
		} else if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		} else if attemptor != nil {
			return nil, attemptor.ToAPIError(txErr)
		}
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

type UpdateEnrollmentModeParams struct {
	EnrollmentMode       string
	OrganizationID       string
	OrganizationDomainID string
	DeletePending        *bool
}

func (params UpdateEnrollmentModeParams) validate(domainSettings organizationsettings.DomainsSettings) apierror.Error {
	if !constants.OrganizationDomainEnrollmentModes.Contains(params.EnrollmentMode) {
		return apierror.FormInvalidParameterValueWithAllowed(param.OrgDomainEnrollmentMode.Name, params.EnrollmentMode, constants.OrganizationDomainEnrollmentModes.Array())
	}

	if params.EnrollmentMode == constants.EnrollmentModeAutomaticInvitation && !domainSettings.AutomaticInvitationEnabled() {
		return apierror.OrganizationDomainEnrollmentModeNotEnabled(params.EnrollmentMode)
	}

	if params.EnrollmentMode == constants.EnrollmentModeAutomaticSuggestion && !domainSettings.AutomaticSuggestionEnabled() {
		return apierror.OrganizationDomainEnrollmentModeNotEnabled(params.EnrollmentMode)
	}

	return nil
}

func (s *Service) UpdateEnrollmentMode(ctx context.Context, params UpdateEnrollmentModeParams) (*serialize.OrganizationDomainResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)

	if apiErr := params.validate(env.AuthConfig.OrganizationSettings.Domains); apiErr != nil {
		return nil, apiErr
	}

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionDomainsManage, user.ID); apiErr != nil {
		return nil, apiErr
	}

	if params.EnrollmentMode == constants.EnrollmentModeAutomaticInvitation {
		org, err := s.organizationRepo.FindByID(ctx, s.db, params.OrganizationID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		if !org.HasUnlimitedMemberships() {
			return nil, apierror.OrganizationUnlimitedMembershipsRequired()
		}
	}

	orgDomain, err := s.organizationDomainRepo.QueryByIDAndOrganizationID(ctx, s.db, params.OrganizationDomainID, params.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgDomain == nil {
		return nil, apierror.ResourceNotFound()
	}

	var response *serialize.OrganizationDomainResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if params.DeletePending != nil && *params.DeletePending {
			if err := s.deletePendingInvitationsSuggestions(ctx, tx, orgDomain.ID); err != nil {
				return true, err
			}
		}

		orgDomain.EnrollmentMode = params.EnrollmentMode
		if err = s.organizationDomainRepo.UpdateEnrollmentMode(ctx, tx, orgDomain); err != nil {
			return true, err
		}

		orgDomainSerializable, err := s.serializableService.ConvertOrganizationDomain(ctx, tx, orgDomain)
		if err != nil {
			return true, err
		}

		response = serialize.OrganizationDomain(orgDomainSerializable)

		if err := s.eventService.OrganizationDomainUpdated(ctx, tx, env.Instance, response, params.OrganizationID); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

type DeleteParams struct {
	OrganizationID       string
	OrganizationDomainID string
}

func (s *Service) Delete(ctx context.Context, params DeleteParams) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionDomainsManage, user.ID); apiErr != nil {
		return nil, apiErr
	}

	// Ensure organization domain exists
	orgDomain, err := s.organizationDomainRepo.QueryByIDAndOrganizationID(ctx, s.db, params.OrganizationDomainID, params.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if orgDomain == nil {
		return nil, apierror.ResourceNotFound()
	}

	var response *serialize.DeletedObjectResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err := s.organizationInvitationRepo.DeletePendingByOrganizationDomain(ctx, tx, orgDomain.ID); err != nil {
			return true, err
		}

		if err := s.organizationSuggestionRepo.DeletePendingByOrganizationDomain(ctx, tx, orgDomain.ID); err != nil {
			return true, err
		}

		if err = s.organizationDomainRepo.DeleteByID(ctx, tx, orgDomain.ID); err != nil {
			return true, err
		}

		response = serialize.DeletedObject(orgDomain.ID, serialize.ObjectOrganizationDomain)

		if err = s.eventService.OrganizationDomainDeleted(ctx, tx, env.Instance, response, params.OrganizationID); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) autoVerifyIfPossible(ctx context.Context, tx database.Tx, orgDomain *model.OrganizationDomain, user *model.User) error {
	verifiedEmails, err := s.identificationRepo.FindAllByUserAndVerifiedAndTypes(ctx, tx, user.ID, true, constants.ITEmailAddress)
	if err != nil {
		return err
	}

	var email *model.Identification
	for _, verifiedEmail := range verifiedEmails {
		if emailaddress.Domain(verifiedEmail.Identifier.String) == orgDomain.Name {
			email = verifiedEmail
			break
		}
	}

	if email == nil {
		return nil
	}

	verification := &model.OrganizationDomainVerification{OrganizationDomainVerification: &sqbmodel.OrganizationDomainVerification{
		InstanceID:           orgDomain.InstanceID,
		OrganizationID:       orgDomain.OrganizationID,
		OrganizationDomainID: orgDomain.ID,
		Strategy:             constants.StrategyFrom(constants.VSAffiliationEmailCode),
	}}
	if err = s.organizationDomainVerificationRepo.Insert(ctx, tx, verification); err != nil {
		return err
	}

	orgDomain.Verified = true
	orgDomain.AffiliationEmailAddress = null.StringFrom(email.Identifier.String)
	orgDomain.VerificationID = null.StringFrom(verification.ID)
	return s.organizationDomainRepo.Update(ctx, tx, orgDomain, sqbmodel.OrganizationDomainColumns.Verified, sqbmodel.OrganizationDomainColumns.AffiliationEmailAddress, sqbmodel.OrganizationDomainColumns.VerificationID)
}

func (s *Service) deletePendingInvitationsSuggestions(ctx context.Context, tx database.Tx, orgDomainID string) error {
	if err := s.organizationInvitationRepo.DeletePendingByOrganizationDomain(ctx, tx, orgDomainID); err != nil {
		return err
	}

	return s.organizationSuggestionRepo.DeletePendingByOrganizationDomain(ctx, tx, orgDomainID)
}
