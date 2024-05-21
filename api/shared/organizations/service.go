package organizations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/applications"
	"clerk/api/shared/client_data"
	"clerk/api/shared/comms"
	"clerk/api/shared/events"
	"clerk/api/shared/images"
	"clerk/api/shared/pagination"
	"clerk/api/shared/restrictions"
	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/billing"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/emailaddress"
	"clerk/pkg/jobs"
	"clerk/pkg/metadata"
	"clerk/pkg/organizationsettings"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/set"
	clerkstrings "clerk/pkg/strings"
	"clerk/pkg/ticket"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/alexsergivan/transliterator"
	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/types"
)

var systemPermissions = []struct {
	Name        string
	Key         string
	Description string
}{
	{Name: "Manage organization", Key: constants.PermissionOrgManage, Description: "Permission to manage an organization."},
	{Name: "Delete organization", Key: constants.PermissionOrgDelete, Description: "Permission to delete an organization."},
	{Name: "Read members", Key: constants.PermissionMembersRead, Description: "Permission to read the members of an organization."},
	{Name: "Manage members", Key: constants.PermissionMembersManage, Description: "Permission to manage the members of an organization."},
	{Name: "Read domains", Key: constants.PermissionDomainsRead, Description: "Permission to read the domains of an organization."},
	{Name: "Manage domains", Key: constants.PermissionDomainsManage, Description: "Permission to manage the domains of an organization."},
}

var rolePermissionAssociation = map[string][]string{
	constants.RoleAdmin:       constants.AdminPermissions.Array(),
	constants.RoleBasicMember: constants.BasicMemberPermissions.Array(),
}

type Service struct {
	clock     clockwork.Clock
	db        database.Database
	gueClient *gue.Client
	trans     *transliterator.Transliterator

	// services
	applicationDeleter  *applications.Deleter
	comms               *comms.Service
	eventsService       *events.Service
	restrictionsService *restrictions.Service
	userProfileService  *user_profile.Service

	// repositories
	billingPlanRepo             *repository.BillingPlans
	billingSubscriptionRepo     *repository.BillingSubscriptions
	identificationsRepo         *repository.Identification
	organizationsRepo           *repository.Organization
	organizationInvitationsRepo *repository.OrganizationInvitation
	organizationMembershipsRepo *repository.OrganizationMembership
	permissionRepo              *repository.Permission
	roleRepo                    *repository.Role
	rolePermissionRepo          *repository.RolePermission
	subscriptionPlanRepo        *repository.SubscriptionPlans
	userRepo                    *repository.Users
	clientDataService           *client_data.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                       deps.Clock(),
		gueClient:                   deps.GueClient(),
		db:                          deps.DB(),
		trans:                       transliterator.NewTransliterator(nil),
		applicationDeleter:          applications.NewDeleter(deps),
		comms:                       comms.NewService(deps),
		eventsService:               events.NewService(deps),
		restrictionsService:         restrictions.NewService(deps.EmailQualityChecker()),
		userProfileService:          user_profile.NewService(deps.Clock()),
		identificationsRepo:         repository.NewIdentification(),
		organizationsRepo:           repository.NewOrganization(),
		organizationInvitationsRepo: repository.NewOrganizationInvitation(),
		organizationMembershipsRepo: repository.NewOrganizationMembership(),
		permissionRepo:              repository.NewPermission(),
		roleRepo:                    repository.NewRole(),
		rolePermissionRepo:          repository.NewRolePermission(),
		subscriptionPlanRepo:        repository.NewSubscriptionPlans(),
		userRepo:                    repository.NewUsers(),
		clientDataService:           client_data.NewService(deps),
		billingPlanRepo:             repository.NewBillingPlans(),
		billingSubscriptionRepo:     repository.NewBillingSubscriptions(),
	}
}

type CreateParams struct {
	Instance                *model.Instance
	MaxAllowedOrganizations null.Int
	Organization            *model.Organization
	Slug                    *string
	Subscription            *model.Subscription
	OrganizationSettings    organizationsettings.OrganizationSettings
}

func (s *Service) Create(ctx context.Context, tx database.Tx, params CreateParams) apierror.Error {
	apiErr := s.orgsQuotaAvailableForInstance(ctx, tx, params)
	if apiErr != nil {
		return apiErr
	}

	apiErr = s.orgsQuotaAvailableForUser(ctx, tx, params.OrganizationSettings, params.Organization.CreatedBy)
	if apiErr != nil {
		return apiErr
	}

	if params.Slug == nil {
		params.Organization.Slug = s.generateUniqueSlug(params.Organization.Name)
	} else {
		params.Organization.Slug = *params.Slug
	}

	params.Organization.AdminDeleteEnabled = params.OrganizationSettings.Actions.AdminDelete
	params.Organization.Name = strings.TrimSpace(params.Organization.Name)

	return s.createOrg(ctx, tx, createOrgParams{
		org:            params.Organization,
		instance:       params.Instance,
		creatorRole:    params.OrganizationSettings.CreatorRole,
		subscriptionID: params.Subscription.ID,
	})
}

var (
	nonSupportedCharacters = regexp.MustCompile(`[^a-z0-9\-_]+`)
)

func (s *Service) generateUniqueSlug(name string) string {
	slug := s.trans.Transliterate(name, "")
	slug = strings.TrimSpace(strings.ToLower(slug))
	slug = nonSupportedCharacters.ReplaceAllString(slug, "-")
	return fmt.Sprintf("%s-%d", slug, s.clock.Now().UTC().Unix())
}

type DeleteParams struct {
	Organization     *model.Organization
	Env              *model.Env
	RequestingUserID *string
}

// Delete deletes the organization with the given organization id,
// enqueues a background job to delete its logo if present and sends
// the appropriate webhook event message.
func (s *Service) Delete(ctx context.Context, tx database.Tx, params DeleteParams) (*serialize.DeletedObjectResponse, error) {
	org := params.Organization

	if params.Env.Application.Type == string(constants.RTSystem) {
		// We schedule the soft-delete instead of doing it in place, because soft-deletion also
		// involves Stripe cancellation, which is an action that cannot be reverted, if the
		// transaction fails.
		err := s.applicationDeleter.ScheduleSoftDeleteOfOwnedApplications(ctx, tx, org.ID, constants.OrganizationResource)
		if err != nil {
			return nil, err
		}
	}

	if err := s.organizationsRepo.DeleteByID(ctx, tx, org.ID); err != nil {
		return nil, err
	}

	if org.LogoPublicURL.Valid {
		err := s.EnqueueCleanupImageJob(ctx, tx, org.LogoPublicURL.String)
		if err != nil {
			return nil, err
		}
	}

	response := serialize.DeletedObject(org.ID, serialize.ObjectOrganization)

	err := s.eventsService.OrganizationDeleted(ctx, tx, params.Env.Instance, response, params.RequestingUserID)
	if err != nil {
		return nil, err
	}
	return response, nil
}

// orgsQuotaAvailableForInstance checks whether the instance has reached the maximum organization
// creation quota. This hard limit only applied for non-production instances that are on a free
// plan.
func (s *Service) orgsQuotaAvailableForInstance(ctx context.Context, tx database.Tx, params CreateParams) apierror.Error {
	if !params.MaxAllowedOrganizations.Valid || params.Subscription.StripeSubscriptionID.Valid {
		// instance has unlimited organizations or is a paid application
		return nil
	}

	count, err := s.organizationsRepo.CountByInstance(ctx, tx, params.Instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if count >= int64(params.MaxAllowedOrganizations.Int) {
		return apierror.OrganizationQuotaExceeded(params.MaxAllowedOrganizations.Int)
	}
	return nil
}

// Checks that the user with userID has not reached their maximum organization
// creation quota.
func (s *Service) orgsQuotaAvailableForUser(ctx context.Context, tx database.Tx, orgSettings organizationsettings.OrganizationSettings, userID string) apierror.Error {
	count, err := s.organizationsRepo.CountByUser(ctx, tx, userID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if int(count) >= orgSettings.CreateQuotaPerUser {
		return apierror.QuotaExceeded()
	}
	return nil
}

type createOrgParams struct {
	org            *model.Organization
	instance       *model.Instance
	creatorRole    string
	subscriptionID string
}

// createOrg creates the model.Organization record and adds the
// organization creator as an admin.
func (s *Service) createOrg(ctx context.Context, tx database.Tx, params createOrgParams) apierror.Error {
	err := s.organizationsRepo.Insert(ctx, tx, params.org)
	if err != nil {
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueOrganizationSlug) {
			return apierror.FormIdentifierExists("slug")
		}
		return apierror.Unexpected(err)
	}

	err = s.eventsService.OrganizationCreated(ctx, tx, params.instance, serialize.OrganizationBAPI(ctx, params.org))
	if err != nil {
		return apierror.Unexpected(err)
	}

	creatorRole, err := s.roleRepo.FindByKeyAndInstance(ctx, tx, params.creatorRole, params.instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	orgMembership := &model.OrganizationMembership{
		OrganizationMembership: &sqbmodel.OrganizationMembership{
			OrganizationID: params.org.ID,
			UserID:         params.org.CreatedBy,
			InstanceID:     params.instance.ID,
			RoleID:         creatorRole.ID,
		},
	}
	_, apiErr := s.createMembership(ctx, tx, createMembershipParams{
		membership:       orgMembership,
		requestingUserID: orgMembership.UserID,
		instance:         params.instance,
		subscriptionID:   params.subscriptionID,
	})
	return apiErr
}

type UpdateParams struct {
	Name                  *string `validate:"omitempty,required,max=256"`
	Slug                  *string
	MaxAllowedMemberships *int  `json:"max_allowed_memberships" form:"max_allowed_memberships" validate:"omitempty,numeric,gte=0"`
	AdminDeleteEnabled    *bool `json:"admin_delete_enabled" form:"admin_delete_enabled"`
	OrganizationID        string
	RequestingUserID      string
	PublicMetadata        *json.RawMessage    `json:"public_metadata" form:"public_metadata"`
	PrivateMetadata       *json.RawMessage    `json:"private_metadata" form:"private_metadata"`
	Instance              *model.Instance     `json:"-"`
	Subscription          *model.Subscription `json:"-"`
}

// Validate that all required attributes are not blank.
func (params UpdateParams) validate() apierror.Error {
	if err := validator.New().Struct(params); err != nil {
		return apierror.FormValidationFailed(err)
	}
	if params.Slug != nil {
		err := validate.OrganizationSlugFormat(*params.Slug, "slug")
		if err != nil {
			return err
		}
	}
	return metadata.Validate(params.toMetadata())
}

func (params UpdateParams) toMetadata() metadata.Metadata {
	v := metadata.Metadata{}
	if params.PrivateMetadata != nil {
		v.Private = *params.PrivateMetadata
	}
	if params.PublicMetadata != nil {
		v.Public = *params.PublicMetadata
	}
	return v
}

func (s *Service) Update(ctx context.Context, tx database.Tx, params UpdateParams) (*model.Organization, error) {
	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	organization, err := s.organizationsRepo.FindByID(ctx, tx, params.OrganizationID)
	if err != nil {
		return nil, err
	}

	if params.Name != nil {
		organization.Name = strings.TrimSpace(*params.Name)
	}
	if params.Slug != nil {
		organization.Slug = *params.Slug
	}
	if params.MaxAllowedMemberships != nil {
		organization.MaxAllowedMemberships = *params.MaxAllowedMemberships
	}
	if params.AdminDeleteEnabled != nil {
		organization.AdminDeleteEnabled = *params.AdminDeleteEnabled
	}
	if params.PrivateMetadata != nil {
		organization.PrivateMetadata = types.JSON(*params.PrivateMetadata)
	}
	if params.PublicMetadata != nil {
		organization.PublicMetadata = types.JSON(*params.PublicMetadata)
	}

	if !params.Instance.HasAccessToAllFeatures() {
		plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, params.Subscription.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		if params.MaxAllowedMemberships != nil {
			unsupportedFeature := billing.ValidateAllowedMemberships(organization.MaxAllowedMemberships, plans...)
			if unsupportedFeature != "" {
				return nil, apierror.UnsupportedSubscriptionPlanFeatures([]string{unsupportedFeature})
			}
		}
	}

	err = s.organizationsRepo.Update(ctx, tx, organization)
	if err != nil {
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueOrganizationSlug) {
			return nil, apierror.FormIdentifierExists("slug")
		}
		return nil, err
	}

	err = s.eventsService.OrganizationUpdated(ctx, tx, params.Instance, serialize.OrganizationBAPI(ctx, organization), &params.RequestingUserID)
	if err != nil {
		return nil, err
	}

	return organization, nil
}

type CreateMembershipParams struct {
	OrganizationID   string
	UserID           string
	Role             string
	RequestingUserID string
	Instance         *model.Instance
	Subscription     *model.Subscription
}

func (s *Service) CreateMembership(ctx context.Context, tx database.Tx, params CreateMembershipParams) (*model.OrganizationMembershipSerializable, apierror.Error) {
	user, err := s.userRepo.QueryByIDAndInstance(ctx, tx, params.UserID, params.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(params.UserID)
	}

	role, err := s.roleRepo.QueryByKeyAndInstance(ctx, tx, params.Role, params.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if role == nil {
		return nil, apierror.OrganizationRoleNotFound(param.Role.Name)
	}

	membership := &model.OrganizationMembership{
		OrganizationMembership: &sqbmodel.OrganizationMembership{
			OrganizationID: params.OrganizationID,
			UserID:         params.UserID,
			InstanceID:     params.Instance.ID,
			RoleID:         role.ID,
		},
	}

	serializable, apiErr := s.createMembership(ctx, tx, createMembershipParams{
		membership:       membership,
		requestingUserID: params.RequestingUserID,
		instance:         params.Instance,
		subscriptionID:   params.Subscription.ID,
	})
	if apiErr != nil {
		return nil, apiErr
	}

	return serializable, nil
}

type createMembershipParams struct {
	membership       *model.OrganizationMembership
	requestingUserID string
	instance         *model.Instance
	subscriptionID   string
}

func (s *Service) createMembership(ctx context.Context, tx database.Tx, params createMembershipParams) (*model.OrganizationMembershipSerializable, apierror.Error) {
	apiErr := s.checkMembershipLimit(ctx, tx, params.instance, params.membership.OrganizationID, params.subscriptionID, 1)
	if apiErr != nil {
		return nil, apiErr
	}

	exists, err := s.organizationMembershipsRepo.ExistsByOrganizationAndUser(ctx, tx, params.membership.OrganizationID, params.membership.UserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if exists {
		return nil, apierror.AlreadyAMemberOfOrganization(params.membership.UserID)
	}

	// add new membership in DB
	if err := s.organizationMembershipsRepo.Insert(ctx, tx, params.membership); err != nil {
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueOrganizationIDUserID) {
			return nil, apierror.AlreadyAMemberOfOrganization(params.membership.UserID)
		}
		return nil, apierror.Unexpected(err)
	}

	// send the webhook event
	membershipWithDeps, err := s.organizationMembershipsRepo.QueryByOrganizationAndUser(ctx, tx, params.membership.OrganizationID, params.membership.UserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	serializable, err := s.ConvertToSerializable(ctx, tx, membershipWithDeps)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	payload := serialize.OrganizationMembership(ctx, serializable)
	err = s.eventsService.OrganizationMembershipCreated(ctx, tx, params.instance, payload, params.membership.OrganizationID, params.requestingUserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// register active organization if applicable
	err = s.EmitActiveOrganizationEventIfNeeded(ctx, tx, params.membership.OrganizationID, params.instance)
	if err != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and continue
		sentryclerk.CaptureException(ctx, err)
	}
	return serializable, nil
}

func (s *Service) checkMembershipLimit(
	ctx context.Context,
	tx database.Executor,
	instance *model.Instance,
	organizationID string,
	subscriptionID string,
	pendingMemberships int,
) apierror.Error {
	numMembers, err := s.organizationMembershipsRepo.CountByOrganization(ctx, tx, organizationID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	numPendingInvitations, err := s.organizationInvitationsRepo.CountPendingNonOrgDomainByOrganization(ctx, tx, organizationID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	// We consider pending invitations as memberships as well.
	// The reason is that if we don't, then the membership limit error will be visible to the user who
	// accepts the invitation (who probably will be a basic member) and not to the one who's
	// creating the invitation, i.e. admin.
	totalMemberships := numMembers + numPendingInvitations + int64(pendingMemberships)

	// check if adding a new member exceeds the max allowed memberships of the organization
	organization, err := s.organizationsRepo.FindByID(ctx, tx, organizationID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if organization.MaxAllowedMemberships > 0 && totalMemberships > int64(organization.MaxAllowedMemberships) {
		return apierror.OrganizationMembershipQuotaExceeded(organization.MaxAllowedMemberships)
	}

	// Before checking the subscription limits, check if we have a
	// non-production instance.
	// Membership limits do not apply to non-production instances.
	if !instance.IsProduction() {
		return nil
	}

	// check if adding the new member violates the membership limit of the application's subscription plans
	plans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, tx, subscriptionID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	subscriptionMembershipLimit := model.MaxAllowedOrganizationMemberships(plans)
	if subscriptionMembershipLimit > 0 && totalMemberships > int64(subscriptionMembershipLimit) {
		return apierror.OrganizationMembershipPlanQuotaExceeded(subscriptionMembershipLimit)
	}

	return nil
}

type DeleteMembershipParams struct {
	OrganizationID   string
	UserID           string
	RequestingUserID string
	Env              *model.Env
}

func (s *Service) DeleteMembership(ctx context.Context, params DeleteMembershipParams) (*model.OrganizationMembershipSerializable, apierror.Error) {
	var membership *model.OrganizationMembershipWithDeps
	// The following are read operations but we use a transaction for read consistency
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		// Load the membership we're trying to delete
		membership, err = s.organizationMembershipsRepo.QueryByOrganizationAndUser(ctx, tx, params.OrganizationID, params.UserID)
		if err != nil {
			return true, apierror.Unexpected(err)
		} else if membership == nil {
			return true, apierror.ResourceNotFound()
		}

		// Check at least one other member has the required system permissions
		if err := s.EnsureAtLeastOneWithMinimumSystemPermissions(ctx, tx, membership, params.UserID); err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	// Remove the active organization of any user's active session
	userActiveSessions, err := s.clientDataService.FindAllUserSessions(ctx, params.Env.Instance.ID, params.UserID, client_data.SessionFilterActiveOnly())
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	for _, session := range userActiveSessions {
		if session.ActiveOrganizationID.Valid && session.ActiveOrganizationID.String == params.OrganizationID {
			session.ActiveOrganizationID = null.StringFromPtr(nil)
			if err := s.clientDataService.UpdateSessionActiveOrganizationID(ctx, session); err != nil {
				return nil, apierror.Unexpected(err)
			}
		}
	}

	var serializableMembership *model.OrganizationMembershipSerializable
	txErr = s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// Delete the membership
		if err := s.organizationMembershipsRepo.DeleteByID(ctx, tx, membership.OrganizationMembership.ID); err != nil {
			return true, apierror.Unexpected(err)
		}

		serializableMembership, err = s.ConvertToSerializable(ctx, tx, membership)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		err = s.eventsService.OrganizationMembershipDeleted(ctx, tx, params.Env.Instance,
			serialize.OrganizationMembership(ctx, serializableMembership),
			params.OrganizationID, params.RequestingUserID)
		if err != nil {
			return true, apierror.Unexpected(err)
		}

		existsMember, err := s.organizationMembershipsRepo.ExistsByOrganization(ctx, tx, membership.OrganizationID)
		if err != nil {
			return true, err
		}
		if !existsMember {
			// no members left in the organization, let's delete it
			_, err := s.Delete(ctx, tx, DeleteParams{
				Organization:     &membership.Organization,
				Env:              params.Env,
				RequestingUserID: &params.RequestingUserID,
			})
			if err != nil {
				return true, err
			}
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serializableMembership, nil
}

// ListMembershipsParams holds the organization ID, user ID and
// pagination options for listing an organization's memberships.
type ListMembershipsParams struct {
	OrganizationID *string
	UserID         *string
	Roles          []string
}

func (params ListMembershipsParams) validate() apierror.Error {
	if (params.OrganizationID == nil || *params.OrganizationID == "") && (params.UserID == nil || *params.UserID == "") {
		return apierror.FormMissingParameter("user_id or organization_id")
	}
	return nil
}

// ListMemberships retrieves a list of all organization members for the
// organization specified by params.OrganizationID.
// The method supports pagination on the results.
func (s *Service) ListMemberships(
	ctx context.Context,
	exec database.Executor,
	params ListMembershipsParams,
	paginationParams pagination.Params,
) ([]*model.OrganizationMembershipSerializable, apierror.Error) {
	// Validate parameters
	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	// Retrieve all members
	orgMemberships, err := s.organizationMembershipsRepo.FindAllByUserOrganizationAndRole(ctx, exec, params.UserID, params.OrganizationID, params.Roles, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// Serialize results
	res := make([]*model.OrganizationMembershipSerializable, len(orgMemberships))
	for i, orgMembership := range orgMemberships {
		res[i], err = s.ConvertToSerializable(ctx, exec, orgMembership)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}
	return res, nil
}

type UpdateMembershipParams struct {
	OrganizationID   string
	UserID           string
	Role             string
	RequestingUserID string
	Instance         *model.Instance
}

func (s *Service) UpdateMembership(ctx context.Context, tx database.Tx, params UpdateMembershipParams) (*model.OrganizationMembershipSerializable, apierror.Error) {
	// Ensure user is already part of the organization.
	orgMembership, err := s.organizationMembershipsRepo.QueryByOrganizationAndUser(ctx, tx, params.OrganizationID, params.UserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if orgMembership == nil {
		return nil, apierror.ResourceNotFound()
	}

	role, err := s.roleRepo.QueryByKeyAndInstance(ctx, tx, params.Role, params.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if role == nil {
		return nil, apierror.OrganizationRoleNotFound(param.Role.Name)
	}

	if orgMembership.RoleID != role.ID {
		// Check at least one other member has the required system permissions
		if err := s.EnsureAtLeastOneWithMinimumSystemPermissions(ctx, tx, orgMembership, params.UserID); err != nil {
			return nil, err
		}
	}

	orgMembership.RoleID = role.ID
	err = s.organizationMembershipsRepo.UpdateRole(ctx, tx, &orgMembership.OrganizationMembership)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	// We need to 'reload' the records as the role_id might have changed and thus we need to
	// fetch the latest Role association record
	orgMembership, err = s.organizationMembershipsRepo.QueryByOrganizationAndUser(ctx, tx, params.OrganizationID, params.UserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if orgMembership == nil {
		return nil, apierror.ResourceNotFound()
	}

	serializable, err := s.ConvertToSerializable(ctx, tx, orgMembership)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	err = s.eventsService.OrganizationMembershipUpdated(ctx, tx, params.Instance, serialize.OrganizationMembership(ctx, serializable), params.OrganizationID, params.RequestingUserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serializable, nil
}

func (s *Service) EnsureAtLeastOneWithMinimumSystemPermissions(
	ctx context.Context,
	exec database.Executor,
	orgMembership *model.OrganizationMembershipWithDeps,
	userID string,
) apierror.Error {
	members, err := s.organizationMembershipsRepo.FindAllByOrganizationAndPermissions(ctx, exec, orgMembership.OrganizationID, constants.MinRequiredOrgPermissions.Array())
	if err != nil {
		return apierror.Unexpected(err)
	}

	// If there is only one member with the minimum required permissions,
	// make sure it isn't the user we are deleting/updating the role for.
	if len(members) == 1 && members[0].UserID == userID {
		return apierror.OrganizationMinimumPermissionsNeeded()
	}

	return nil
}

func (s *Service) EnsureMinimumSystemPermissions(permissions []*model.Permission) apierror.Error {
	permKeys := set.New[string]()
	for _, perm := range permissions {
		permKeys.Insert(perm.Key)
	}

	if !constants.MinRequiredOrgPermissions.IsSubset(permKeys) {
		return apierror.OrganizationMissingCreatorRolePermissions(constants.MinRequiredOrgPermissions.Array()...)
	}

	return nil
}

type AcceptInvitationParams struct {
	InvitationID string
	UserID       string
	Instance     *model.Instance
	Subscription *model.Subscription
}

func (s *Service) AcceptInvitation(ctx context.Context, tx database.Tx, params AcceptInvitationParams) (*model.OrganizationInvitationSerializable, error) {
	invitation, err := s.organizationInvitationsRepo.FindByID(ctx, tx, params.InvitationID)
	if err != nil {
		return nil, fmt.Errorf("organizations/acceptInvitation: retrieving organization invitation with id %s: %w",
			params.InvitationID, err)
	}

	role, err := s.roleRepo.QueryByIDAndInstance(ctx, tx, invitation.RoleID.String, params.Instance.ID)
	if err != nil {
		return nil, fmt.Errorf("organizations/acceptInvitation: retrieving organization role with id %s: %w", invitation.RoleID.String, err)
	}
	if role == nil {
		return nil, apierror.OrganizationRoleNotFound(param.Role.Name)
	}

	// accept invitation
	invitation.Status = constants.StatusAccepted
	err = s.organizationInvitationsRepo.UpdateStatus(ctx, tx, invitation)
	if err != nil {
		return nil, fmt.Errorf("organizations/acceptInvitation: changing status of invitation %s to %s: %w",
			invitation.ID, constants.StatusAccepted, err)
	}

	var orgMembership *model.OrganizationMembership

	// Check if the user is already a member, otherwise create a
	// new membership.
	orgMembershipWithUser, err := s.organizationMembershipsRepo.QueryByOrganizationAndUser(ctx, tx, invitation.OrganizationID, params.UserID)
	if err != nil {
		return nil, fmt.Errorf("organizations/acceptInvitation: checking for existing membership for user %s: %w", params.UserID, err)
	}
	if orgMembershipWithUser == nil {
		orgMembership = &model.OrganizationMembership{
			OrganizationMembership: &sqbmodel.OrganizationMembership{
				OrganizationID:  invitation.OrganizationID,
				UserID:          params.UserID,
				PublicMetadata:  invitation.PublicMetadata,
				PrivateMetadata: invitation.PrivateMetadata,
				InstanceID:      params.Instance.ID,
				RoleID:          role.ID,
			},
		}
		_, err := s.createMembership(ctx, tx, createMembershipParams{
			membership:       orgMembership,
			requestingUserID: orgMembership.UserID,
			instance:         params.Instance,
			subscriptionID:   params.Subscription.ID,
		})
		if err != nil {
			return nil, err
		}
	} else {
		orgMembership = &orgMembershipWithUser.OrganizationMembership
	}

	invitation.UserID = null.StringFrom(params.UserID)
	invitation.OrganizationMembershipID = null.StringFrom(orgMembership.ID)
	if err := s.organizationInvitationsRepo.Update(
		ctx, tx, invitation,
		sqbmodel.OrganizationInvitationColumns.UserID,
		sqbmodel.OrganizationInvitationColumns.OrganizationMembershipID); err != nil {
		return nil, fmt.Errorf("organizations/acceptInvitation: updating organization (user=%s, membership=%s): %w",
			params.UserID, orgMembership.ID, err)
	}

	invitationSerializable, err := s.convertOrganizationInvitation(ctx, tx, invitation)
	if err != nil {
		return nil, fmt.Errorf("organizations/acceptInvitation: convert invitation to serializable %+v: %w", invitation, err)
	}

	err = s.eventsService.OrganizationInvitationAccepted(ctx, tx, params.Instance, serialize.OrganizationInvitationBAPI(invitationSerializable), orgMembership.UserID)
	if err != nil {
		return nil, fmt.Errorf("organizations/acceptInvitation: registering event for accepting invitation %+v: %w", invitation, err)
	}

	return invitationSerializable, nil
}

// CreateInvitationParams contains everything you need to create a single organization invitation,
// and create and send organization_invitation.created events and
// the email to the invited user.
type CreateInvitationParams struct {
	EmailAddress     string
	PublicMetadata   *json.RawMessage
	PrivateMetadata  *json.RawMessage
	Role             string
	RedirectURL      *string
	InviterID        string `validate:"required"`
	InviterName      string
	OrganizationName string
	DevBrowserID     *string
}

type CreateInvitationsParams []CreateInvitationParams

func (p CreateInvitationParams) validate() apierror.Error {
	var formErrors apierror.Error

	if err := validator.New().Struct(p); err != nil {
		formErrors = apierror.Combine(formErrors, apierror.FormValidationFailed(err))
	}

	if err := validate.EmailAddress(p.EmailAddress, param.EmailAddress.Name); err != nil {
		formErrors = apierror.Combine(formErrors, err)
	}
	if p.RedirectURL != nil {
		if _, err := url.ParseRequestURI(*p.RedirectURL); err != nil {
			formErrors = apierror.Combine(formErrors, apierror.FormInvalidTypeParameter(param.RedirectURL.Name, "valid url"))
		}
	}

	metadataFields := metadata.Metadata{}
	if p.PublicMetadata != nil {
		metadataFields.Public = *p.PublicMetadata
	}
	if p.PrivateMetadata != nil {
		metadataFields.Private = *p.PrivateMetadata
	}
	if errs := metadata.Validate(metadataFields); errs != nil {
		formErrors = apierror.Combine(formErrors, errs)
	}

	return formErrors
}

func (p CreateInvitationsParams) roleKeys() set.Set[string] {
	roleKeys := set.New[string]()
	for _, params := range p {
		roleKeys.Insert(params.Role)
	}

	return roleKeys
}

// CreateAndSendInvitations creates organization invitations in bulk,
// triggers organization_invitation.created events and sends the emails
// to the invited users.
func (s *Service) CreateAndSendInvitations(
	ctx context.Context,
	tx database.Tx,
	params CreateInvitationsParams,
	organizationID string,
	env *model.Env,
) ([]*model.OrganizationInvitationSerializable, error) {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	if !userSettings.IsEnabled(names.EmailAddress) {
		return nil, apierror.InvitationsNotSupportedInInstance()
	}

	// Validate the parameters
	apiErr := s.validateCreateInvitationParams(ctx, tx, params, env.Instance.ID)
	if apiErr != nil {
		return nil, apiErr
	}

	// Make sure all the users have the necessary permission
	inviterUserIDs := set.New[string]()
	for _, p := range params {
		inviterUserIDs.Insert(p.InviterID)
	}

	if apiErr := s.EnsureHasAccess(ctx, tx, organizationID, constants.PermissionMembersManage, inviterUserIDs.Array()...); apiErr != nil {
		return nil, apiErr
	}

	emailAddressParam := userSettings.GetAttribute(names.EmailAddress)
	emailAddresses := make([]string, len(params))
	for i, p := range params {
		// Normalize before we search for duplicates.
		emailAddresses[i], apiErr = emailAddressParam.Sanitize(p.EmailAddress, param.EmailAddress.Name)
		if apiErr != nil {
			return nil, apiErr
		}
	}

	// check whether email addresses are allowed based on the instance restrictions, i.e. allowlists/blocklists
	if apiErr := s.checkRestrictions(ctx, tx, env, userSettings, emailAddresses); apiErr != nil {
		return nil, apiErr
	}

	if len(params) > constants.MaxBulkSize {
		return nil, apierror.BulkSizeExceeded()
	}

	// Check organization membership limit is respected.
	membersLimitCheckErr := s.checkMembershipLimit(ctx, tx, env.Instance, organizationID, env.Subscription.ID, len(params))
	if membersLimitCheckErr != nil {
		return nil, membersLimitCheckErr
	}

	pendingInvitations, err := s.organizationInvitationsRepo.FindAllPendingByOrganizationAndEmailAddress(ctx, tx, organizationID, emailAddresses)
	if err != nil {
		return nil, err
	}
	pendingInvitationsByEmail := make(map[string]*model.OrganizationInvitation, len(pendingInvitations))
	for _, invitation := range pendingInvitations {
		pendingInvitationsByEmail[strings.ToLower(invitation.EmailAddress)] = invitation
	}

	organization, err := s.organizationsRepo.FindByID(ctx, tx, organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	roleByKey, err := s.buildRoleByKeyAssociation(ctx, tx, params.roleKeys(), env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	invitations := make([]*model.OrganizationInvitationSerializable, len(params))
	for i, p := range params {
		emailAddress := strings.ToLower(p.EmailAddress)

		existingUserID, err := s.findExistingUserForEmailAddress(ctx, tx, env.Instance.ID, emailAddress)
		if err != nil {
			return nil, err
		}
		if existingUserID != "" {
			// if there is an existing user with this email,
			// don't create an organization invitation if they are already an organization member
			membershipExists, err := s.organizationMembershipsRepo.ExistsByOrganizationAndUser(ctx, tx, organizationID, existingUserID)
			if err != nil {
				return nil, apierror.Unexpected(err)
			}
			if membershipExists {
				return nil, apierror.AlreadyAMemberOfOrganization(existingUserID)
			}
		}

		var invitation *model.OrganizationInvitation
		newInvitationCreated := false
		if pendingInvitation, ok := pendingInvitationsByEmail[emailAddress]; ok {
			// if there is already a pending invitation for this email,
			// reuse the existing invitation and just resend the org invitation email
			invitation = pendingInvitation
		} else {
			invitation = &model.OrganizationInvitation{
				OrganizationInvitation: &sqbmodel.OrganizationInvitation{
					InstanceID:     env.Instance.ID,
					EmailAddress:   emailAddress,
					OrganizationID: organizationID,
					Status:         constants.StatusPending,
				},
			}
			newInvitationCreated = true
		}

		invitationRole := roleByKey[p.Role]
		invitation.DevBrowserID = null.StringFromPtr(p.DevBrowserID)
		invitation.RoleID = null.StringFrom(invitationRole.ID)

		if p.PublicMetadata != nil {
			invitation.PublicMetadata = types.JSON(*p.PublicMetadata)
		}
		if p.PrivateMetadata != nil {
			invitation.PrivateMetadata = types.JSON(*p.PrivateMetadata)
		}

		if existingUserID != "" {
			invitation.UserID = null.StringFrom(existingUserID)
		}

		if newInvitationCreated {
			if err := s.organizationInvitationsRepo.Insert(ctx, tx, invitation); err != nil {
				return nil, fmt.Errorf("inserting new org invitation %+v: %w", invitation.OrganizationInvitation, err)
			}
		}

		invitationSerializable := &model.OrganizationInvitationSerializable{
			OrganizationInvitation: invitation,
			Role:                   invitationRole,
		}

		if newInvitationCreated {
			serializedInvitation := serialize.OrganizationInvitationBAPI(invitationSerializable)
			if err := s.eventsService.OrganizationInvitationCreated(ctx, tx, env.Instance, serializedInvitation, p.InviterID); err != nil {
				return nil, fmt.Errorf("sending organization invitation created event for %+v: %w", serializedInvitation, err)
			}
		}

		claims := ticket.Claims{
			InstanceID:     invitation.InstanceID,
			SourceType:     constants.OSTOrganizationInvitation,
			SourceID:       invitation.ID,
			OrganizationID: &organizationID,
			RedirectURL:    p.RedirectURL,
		}
		accessToken, err := ticket.Generate(claims, env.Instance, s.clock)
		if err != nil {
			return nil, fmt.Errorf("generating access token for claims %+v: %w", claims, err)
		}

		fapiURL := env.Domain.FapiURL()
		clerkJSVersion := clerkjs_version.FromContext(ctx)
		actionLink, err := createInvitationLink(accessToken, fapiURL, clerkJSVersion)
		if err != nil {
			return nil, fmt.Errorf("creating invitation link for %s: %w", fapiURL, err)
		}

		if err := s.comms.SendOrganizationInvitationEmail(ctx, tx, env, comms.EmailOrganizationInvitation{
			Organization: organization,
			Invitation:   invitation,
			InviterName:  p.InviterName,
			ActionURL:    actionLink,
		}); err != nil {
			return nil, fmt.Errorf("orgInvitations/create: sending org invitation email to %s: %w", invitation.EmailAddress, err)
		}

		invitations[i] = invitationSerializable
	}
	return invitations, nil
}

func (s *Service) validateCreateInvitationParams(ctx context.Context, tx database.Tx, params CreateInvitationsParams, instanceID string) apierror.Error {
	var errors apierror.Error
	for _, p := range params {
		if err := p.validate(); err != nil {
			errors = apierror.Combine(errors, err)
		}
	}

	if apiErr := s.validateInvitationRoles(ctx, tx, params.roleKeys(), instanceID); apiErr != nil {
		errors = apierror.Combine(errors, apiErr)
	}

	return errors
}

// validateInvitationRoles checks that every provided role exists on the given instance
func (s *Service) validateInvitationRoles(ctx context.Context, tx database.Tx, roleKeys set.Set[string], instanceID string) apierror.Error {
	totalRoles, err := s.roleRepo.CountByInstanceAndKeys(ctx, tx, instanceID, roleKeys.Array())
	if err != nil {
		return apierror.Unexpected(err)
	}
	if roleKeys.Count() != totalRoles {
		return apierror.OrganizationRoleNotFound(param.Role.Name)
	}

	return nil
}

func (s *Service) buildRoleByKeyAssociation(ctx context.Context, tx database.Tx, roleKeys set.Set[string], instanceID string) (map[string]*model.Role, error) {
	roles, err := s.roleRepo.FindAllByInstanceAndKeys(ctx, tx, instanceID, roleKeys.Array())
	if err != nil {
		return nil, err
	}

	roleByKey := make(map[string]*model.Role)
	for _, role := range roles {
		roleByKey[role.Key] = role
	}

	return roleByKey, nil
}

func (s *Service) buildRoleByIDAssociation(ctx context.Context, exec database.Executor, roleIDs set.Set[string], instanceID string) (map[string]*model.Role, error) {
	roles, err := s.roleRepo.FindAllByInstanceAndIDs(ctx, exec, instanceID, roleIDs.Array())
	if err != nil {
		return nil, err
	}

	roleByID := make(map[string]*model.Role)
	for _, role := range roles {
		roleByID[role.ID] = role
	}

	return roleByID, nil
}

func (s *Service) checkRestrictions(ctx context.Context, tx database.Tx, env *model.Env, userSettings *usersettings.UserSettings, emailAddresses []string) apierror.Error {
	restrictionIdents := make([]restrictions.Identification, 0)
	for _, emailAddress := range emailAddresses {
		restrictionIdents = append(restrictionIdents, restrictions.Identification{
			Identifier:          emailAddress,
			CanonicalIdentifier: emailaddress.Canonical(emailAddress),
			Type:                constants.ITEmailAddress,
		})
	}

	res, err := s.restrictionsService.CheckAll(
		ctx,
		tx,
		restrictionIdents,
		restrictions.Settings{
			Restrictions: userSettings.Restrictions,
			TestMode:     env.AuthConfig.TestMode,
		},
		env.Instance.ID,
	)
	if err != nil {
		return apierror.Unexpected(err)
	}

	if len(res.Offenders()) > 0 {
		return apierror.IdentifierNotAllowedAccess(res.Offenders()...)
	}
	return nil
}

func createInvitationLink(ticket, fapiURL, clerkJSVersion string) (string, error) {
	link, err := url.Parse(fapiURL)
	if err != nil {
		return "", err
	}

	link = link.JoinPath("/v1/tickets/accept")
	query := link.Query()
	query.Set("ticket", ticket)
	if clerkJSVersion != "" {
		query.Set(param.ClerkJSVersion, clerkJSVersion)
	}
	link.RawQuery = query.Encode()
	return link.String(), nil
}

func (s *Service) findExistingUserForEmailAddress(ctx context.Context, tx database.Tx, instanceID, emailAddress string) (string, error) {
	ident, err := s.identificationsRepo.QueryClaimedVerifiedByInstanceAndIdentifierAndType(ctx, tx, instanceID, emailAddress, constants.ITEmailAddress)
	if err != nil {
		return "", err
	}
	if ident != nil && ident.UserID.Valid {
		return ident.UserID.String, nil
	}

	return "", nil
}

// ListInvitations retrieves a list of invitations, that are not created as part of an Organization Domain,
// for the provided organization ID and filtered by status. Results will be limited by Limit and Offset.
// Only organization admins can retrieve the list of invitations.
func (s *Service) ListInvitations(ctx context.Context, exec database.Executor, instanceID, organizationID string, statuses []string, paginationParams pagination.Params) ([]*model.OrganizationInvitationSerializable, apierror.Error) {
	invitations, err := s.organizationInvitationsRepo.FindAllNonOrgDomainByOrganizationAndStatus(ctx, exec, organizationID, statuses, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	roleIDs := set.New[string]()
	for _, invitation := range invitations {
		if invitation.RoleID.Valid {
			roleIDs.Insert(invitation.RoleID.String)
		}
	}

	roleByID, err := s.buildRoleByIDAssociation(ctx, exec, roleIDs, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	invitationsSerializable := make([]*model.OrganizationInvitationSerializable, len(invitations))
	for i, invitation := range invitations {
		serializable := &model.OrganizationInvitationSerializable{OrganizationInvitation: invitation}
		if role, ok := roleByID[invitation.RoleID.String]; ok {
			serializable.Role = role
		}

		invitationsSerializable[i] = serializable
	}

	return invitationsSerializable, nil
}

type InvitationSerializableWithOrg struct {
	Serializable *model.OrganizationInvitationSerializable
	Organization *model.Organization
}

func (s *Service) ListInvitationsForUser(ctx context.Context, exec database.Executor, instanceID, userID string, statuses []string, paginationParams pagination.Params) ([]*InvitationSerializableWithOrg, error) {
	invitations, err := s.organizationInvitationsRepo.FindAllByInstanceAndUserAndStatus(ctx, exec, instanceID, userID, statuses, paginationParams)
	if err != nil {
		return nil, err
	}

	roleIDs := set.New[string]()
	for _, invitation := range invitations {
		if invitation.RoleID.Valid {
			roleIDs.Insert(invitation.RoleID.String)
		}
	}

	roleByID, err := s.buildRoleByIDAssociation(ctx, exec, roleIDs, instanceID)
	if err != nil {
		return nil, err
	}

	invitationsSerializable := make([]*InvitationSerializableWithOrg, len(invitations))
	for i, invitation := range invitations {
		serializable := &InvitationSerializableWithOrg{
			Serializable: &model.OrganizationInvitationSerializable{OrganizationInvitation: &invitation.OrganizationInvitation},
			Organization: &invitation.Organization,
		}
		if role, ok := roleByID[invitation.RoleID.String]; ok {
			serializable.Serializable.Role = role
		}

		invitationsSerializable[i] = serializable
	}

	return invitationsSerializable, nil
}

func (s *Service) ReadInvitation(ctx context.Context, exec database.Executor, organizationID, invitationID string) (*model.OrganizationInvitationSerializable, apierror.Error) {
	invitation, err := s.organizationInvitationsRepo.QueryByIDAndOrganizationID(ctx, exec, invitationID, organizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if invitation == nil {
		return nil, apierror.ResourceNotFound()
	}

	invitationSerializable, err := s.convertOrganizationInvitation(ctx, exec, invitation)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return invitationSerializable, nil
}

type RevokeInvitationParams struct {
	InvitationID     string
	OrganizationID   string
	RequestingUserID string
}

// RevokeInvitation attempts to change an invitation's status to revoked.
// The invitation is fetched by params.InvitationID and needs to be pending.
// The requesting user must be an admin in the organization specified by
// params.OrganizationID.
func (s *Service) RevokeInvitation(
	ctx context.Context,
	exec database.Executor,
	params RevokeInvitationParams,
	instance *model.Instance,
) (*model.OrganizationInvitationSerializable, apierror.Error) {
	if apiErr := s.EnsureHasAccess(ctx, exec, params.OrganizationID, constants.PermissionMembersManage, params.RequestingUserID); apiErr != nil {
		return nil, apiErr
	}

	// Fetch invitation, check that it's pending
	invitation, err := s.organizationInvitationsRepo.QueryByIDAndOrganizationID(ctx, exec, params.InvitationID, params.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if invitation == nil || !invitation.IsPending() {
		return nil, apierror.OrganizationInvitationNotPending()
	}

	// Update status
	invitation.Status = constants.StatusRevoked
	err = s.organizationInvitationsRepo.UpdateStatus(ctx, exec, invitation)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	invitationSerializable, err := s.convertOrganizationInvitation(ctx, exec, invitation)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	err = s.eventsService.OrganizationInvitationRevoked(ctx, exec, instance, serialize.OrganizationInvitationBAPI(invitationSerializable), params.RequestingUserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return invitationSerializable, nil
}

func (s *Service) ConvertToSerializable(
	ctx context.Context,
	exec database.Executor,
	orgMembership *model.OrganizationMembershipWithDeps,
) (*model.OrganizationMembershipSerializable, error) {
	serializable := model.OrganizationMembershipSerializable{
		OrganizationMembership: orgMembership.OrganizationMembership,
		Organization:           orgMembership.Organization,
		Role:                   orgMembership.Role,
		PermissionKeys:         orgMembership.PermissionKeys,
	}

	membersCount, err := s.organizationMembershipsRepo.CountByOrganization(ctx, exec, orgMembership.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("organizations/convertToSerializable: cannot get count of memberships for organization %s: %w",
			orgMembership.OrganizationID, err)
	}
	serializable.MembersCount = int(membersCount)

	pendingInvitationsCount, err := s.organizationInvitationsRepo.CountPendingNonOrgDomainByOrganization(ctx, exec, orgMembership.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("organizations/convertToSerializable: cannot get count of pending invitations for organization %s: %w",
			orgMembership.OrganizationID, err)
	}
	serializable.PendingInvitationsCount = int(pendingInvitationsCount)

	// if user doesn't exist, omit the user, image url, and identifier fields
	if orgMembership.User.User == nil {
		return &serializable, nil
	}

	serializable.User = orgMembership.User
	serializable.ProfileImageURL, _ = s.userProfileService.GetProfileImageURL(&orgMembership.User)
	serializable.ImageURL, err = s.userProfileService.GetImageURL(&orgMembership.User)
	if err != nil {
		return nil, fmt.Errorf("organizations/convertToSerializable: cannot get image url for organization's user %s: %w",
			orgMembership.User.ID, err)
	}

	// Find and set the member's identification
	var identificationID string
	if orgMembership.User.PrimaryEmailAddressID.Valid {
		identificationID = orgMembership.User.PrimaryEmailAddressID.String
	} else if orgMembership.User.PrimaryPhoneNumberID.Valid {
		identificationID = orgMembership.User.PrimaryPhoneNumberID.String
	} else if orgMembership.User.PrimaryWeb3WalletID.Valid {
		identificationID = orgMembership.User.PrimaryWeb3WalletID.String
	}

	var identification *model.Identification
	if identificationID != "" {
		identification, err = s.identificationsRepo.QueryByID(ctx, exec, identificationID)
	} else {
		identification, err = s.identificationsRepo.QueryByTypeAndUser(ctx, exec, constants.ITUsername, orgMembership.UserID)
	}

	if err != nil {
		return nil, fmt.Errorf("organizations/convertToSerializable: cannot get identification with id %s: %w", identificationID, err)
	}

	if identification != nil && identification.TargetIdentificationID.Valid {
		// this is an OAuth identification, so we need to find the connected identification which
		// contains the actual identifier
		ident, err := s.identificationsRepo.FindByID(ctx, exec, identification.TargetIdentificationID.String)
		if err != nil {
			return nil, fmt.Errorf("organizations/convertToSerializable: cannot get target identification with id %s: %w",
				identification.TargetIdentificationID.String, err)
		}
		identification = ident
	}
	if identification != nil {
		serializable.Identifier = identification.Identifier.String
	}

	if orgMembership.Organization.BillingSubscriptionID.Valid {
		plan, err := s.billingPlanRepo.FindBySubscriptionID(ctx, exec, orgMembership.Organization.BillingSubscriptionID.String)
		if err != nil {
			return nil, fmt.Errorf("organizations/convertToSerializable: cannot get plan for subscription %s: %w",
				orgMembership.Organization.BillingSubscriptionID.String, err)
		}

		serializable.BillingPlan = &plan.Key
	}

	return &serializable, nil
}

func (s *Service) convertOrganizationInvitation(ctx context.Context, exec database.Executor, invitation *model.OrganizationInvitation) (*model.OrganizationInvitationSerializable, error) {
	var role *model.Role
	if invitation.RoleID.Valid {
		var err error
		role, err = s.roleRepo.FindByIDAndInstance(ctx, exec, invitation.RoleID.String, invitation.InstanceID)
		if err != nil {
			return nil, fmt.Errorf("organizations/ConvertOrganizationInvitation: cannot get invitation role %s: %w", invitation.RoleID.String, err)
		}
	}

	return &model.OrganizationInvitationSerializable{
		OrganizationInvitation: invitation,
		Role:                   role,
	}, nil
}

// EnsureHasAccess makes sure that the provided users have the required permission to access the specific resource
func (s *Service) EnsureHasAccess(ctx context.Context, exec database.Executor, organizationID, permission string, userIDs ...string) apierror.Error {
	orgMembers, err := s.organizationMembershipsRepo.FindAllByOrganizationAndUserIDsWithPermissions(ctx, exec, organizationID, userIDs)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if len(orgMembers) != len(userIDs) {
		return apierror.NotAMemberInOrganization()
	}

	for _, member := range orgMembers {
		if !set.New(member.PermissionKeys...).Contains(permission) {
			return apierror.MissingOrganizationPermission(permission)
		}
	}

	return nil
}

// EnsureHasAccessAny makes sure that the provided user has ANY of the required permission to access the specific resource
func (s *Service) EnsureHasAccessAny(ctx context.Context, exec database.Executor, organizationID, userID string, permissions ...string) apierror.Error {
	orgMember, err := s.organizationMembershipsRepo.QueryByOrganizationAndUserWithPermissions(ctx, exec, organizationID, userID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if orgMember == nil {
		return apierror.NotAMemberInOrganization()
	}

	memberPermissions := set.New(orgMember.PermissionKeys...)
	for _, permission := range permissions {
		if memberPermissions.Contains(permission) {
			return nil
		}
	}

	return apierror.MissingOrganizationPermission(permissions...)
}

func (s *Service) CreateDefaultRolesAndPermissions(ctx context.Context, tx database.Tx, instanceID string) error {
	// Check if already seeded
	systemPermExists, err := s.permissionRepo.ExistsByInstanceAndType(ctx, tx, instanceID, constants.RTSystem)
	if err != nil {
		return err
	}
	if systemPermExists {
		return nil
	}

	roles, err := s.createDefaultRoles(ctx, tx, instanceID)
	if err != nil {
		return err
	}

	permissions, err := s.createSystemPermissions(ctx, tx, instanceID)
	if err != nil {
		return err
	}

	return s.createRolePermissionAssociation(ctx, tx, roles, permissions, instanceID)
}

func (s *Service) createDefaultRoles(ctx context.Context, tx database.Tx, instanceID string) ([]*model.Role, error) {
	roles := make([]*model.Role, 0)
	for _, role := range model.DefaultRoles() {
		roles = append(roles, &model.Role{Role: &sqbmodel.Role{
			InstanceID:  instanceID,
			Name:        role.Name,
			Key:         role.Key,
			Description: role.Description,
		}})
	}

	if err := s.roleRepo.InsertBulk(ctx, tx, roles); err != nil {
		return nil, err
	}

	return roles, nil
}

func (s *Service) createSystemPermissions(ctx context.Context, tx database.Tx, instanceID string) ([]*model.Permission, error) {
	permissions := make([]*model.Permission, 0)
	for _, perm := range systemPermissions {
		permissions = append(permissions, &model.Permission{Permission: &sqbmodel.Permission{
			InstanceID:  instanceID,
			Name:        perm.Name,
			Key:         perm.Key,
			Description: perm.Description,
			Type:        string(constants.RTSystem),
		}})
	}

	if err := s.permissionRepo.InsertBulk(ctx, tx, permissions); err != nil {
		return nil, err
	}

	return permissions, nil
}

func (s *Service) createRolePermissionAssociation(ctx context.Context, tx database.Tx, roles []*model.Role, permissions []*model.Permission, instanceID string) error {
	roleKeyID := make(map[string]string, 0)
	for _, role := range roles {
		roleKeyID[role.Key] = role.ID
	}

	permissionKeyID := make(map[string]string, 0)
	for _, permission := range permissions {
		permissionKeyID[permission.Key] = permission.ID
	}

	rolePerm := make([]*model.RolePermission, 0)
	for roleKey, permKeys := range rolePermissionAssociation {
		for _, permKey := range permKeys {
			rolePerm = append(rolePerm, &model.RolePermission{RolePermission: &sqbmodel.RolePermission{
				InstanceID:   instanceID,
				RoleID:       roleKeyID[roleKey],
				PermissionID: permissionKeyID[permKey],
			}})
		}
	}

	return s.rolePermissionRepo.InsertBulk(ctx, tx, rolePerm)
}

// EmitActiveOrganizationEventIfNeeded checks whether the given organization should be
// considered as active, and if it should, it would emit an `organization.tapped` event
func (s *Service) EmitActiveOrganizationEventIfNeeded(ctx context.Context, exec database.Executor, organizationID string, instance *model.Instance) error {
	count, err := s.organizationMembershipsRepo.CountByOrganization(ctx, exec, organizationID)
	if err != nil {
		return err
	} else if count < 2 {
		return nil
	}

	err = s.eventsService.OrganizationTapped(ctx, exec, instance, organizationID)
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) EnqueueCleanupImageJob(ctx context.Context, tx database.Tx, publicURL string) error {
	err := jobs.CleanupImage(
		ctx,
		s.gueClient,
		jobs.CleanupImageArgs{
			PublicURL: publicURL,
		},
		jobs.WithTx(tx),
	)
	return err
}

type LogosService struct {
	imagesSvc        *images.Service
	eventsSvc        *events.Service
	organizationsSvc *Service

	organizationsRepo *repository.Organization
}

func NewLogosService(deps clerk.Deps) *LogosService {
	return &LogosService{
		imagesSvc:         images.NewService(deps.StorageClient()),
		eventsSvc:         events.NewService(deps),
		organizationsSvc:  NewService(deps),
		organizationsRepo: repository.NewOrganization(),
	}
}

type UpdateLogoParams struct {
	OrganizationID string
	Image          io.ReadCloser
	Filename       string
	UploaderUserID string
}

func (s *LogosService) Update(ctx context.Context, tx database.Tx, params UpdateLogoParams, instance *model.Instance) (*model.Organization, apierror.Error) {
	org, err := s.organizationsRepo.QueryByIDAndInstance(ctx, tx, params.OrganizationID, instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if org == nil {
		return nil, apierror.ResourceNotFound()
	}

	img, apiErr := s.imagesSvc.Create(
		ctx,
		tx,
		images.ImageParams{
			Filename:           params.Filename,
			Prefix:             images.PrefixUploaded,
			Src:                params.Image,
			UploaderUserID:     params.UploaderUserID,
			UsedByResourceType: clerkstrings.ToPtr(constants.OrganizationResource),
		},
	)
	if apiErr != nil {
		return nil, apiErr
	}

	if org.LogoPublicURL.Valid {
		err := s.organizationsSvc.EnqueueCleanupImageJob(ctx, tx, org.LogoPublicURL.String)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	org.LogoPublicURL = null.StringFrom(img.PublicURL)
	err = s.organizationsRepo.UpdateLogo(ctx, tx, org)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	err = s.eventsSvc.OrganizationUpdated(ctx, tx, instance, serialize.OrganizationBAPI(ctx, org), &params.UploaderUserID)
	if err != nil {
		return nil, apierror.Unexpected(fmt.Errorf("send organization updated event for (%s, %s): %w", instance.ID, org.ID, err))
	}

	return org, nil
}

type DeleteLogoParams struct {
	Organization *model.Organization
	Instance     *model.Instance
	UserID       string
}

func (s *LogosService) Delete(ctx context.Context, tx database.Tx, params DeleteLogoParams) (*model.Organization, apierror.Error) {
	org := params.Organization
	if !org.LogoPublicURL.Valid {
		return nil, apierror.ImageNotFound()
	}

	err := s.organizationsSvc.EnqueueCleanupImageJob(ctx, tx, org.LogoPublicURL.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	org.LogoPublicURL = null.StringFromPtr(nil)
	err = s.organizationsRepo.UpdateLogo(ctx, tx, org)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	err = s.eventsSvc.OrganizationUpdated(ctx, tx, params.Instance, serialize.OrganizationBAPI(ctx, org), &params.UserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return org, nil
}
