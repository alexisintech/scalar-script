package organization_invitations

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	clock clockwork.Clock
	db    database.Database

	// services
	organizationsService *organizations.Service

	// repositories
	organizationRepo           *repository.Organization
	organizationInvitationRepo *repository.OrganizationInvitation
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                      deps.Clock(),
		db:                         deps.DB(),
		organizationsService:       organizations.NewService(deps),
		organizationRepo:           repository.NewOrganization(),
		organizationInvitationRepo: repository.NewOrganizationInvitation(),
	}
}

type CreateInvitationForm struct {
	OrganizationID string
	EmailAddresses []string
	Role           string
}

func (s *Service) Create(ctx context.Context, createForm CreateInvitationForm) ([]*serialize.OrganizationInvitationResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)
	devBrowser := requestingdevbrowser.FromContext(ctx)

	var devBrowserID *string
	if devBrowser != nil {
		devBrowserID = &devBrowser.ID
	}

	organization, err := s.organizationRepo.QueryByIDAndInstance(ctx, s.db, createForm.OrganizationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if organization == nil {
		return nil, apierror.ResourceNotFound()
	}

	if err := validate.EmailAddresses(createForm.EmailAddresses, param.EmailAddress.Name); err != nil {
		return nil, err
	}

	sharedParams := make([]organizations.CreateInvitationParams, len(createForm.EmailAddresses))
	for i, emailAddress := range createForm.EmailAddresses {
		sharedParams[i] = organizations.CreateInvitationParams{
			EmailAddress:     emailAddress,
			Role:             createForm.Role,
			InviterID:        user.ID,
			InviterName:      user.Name(),
			OrganizationName: organization.Name,
			DevBrowserID:     devBrowserID,
		}
	}

	var invitations []*model.OrganizationInvitationSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		invitations, err = s.organizationsService.CreateAndSendInvitations(ctx, tx, sharedParams, createForm.OrganizationID, env)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueOrganizationInvitationPending) ||
			clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueOrganizationInvitationAccepted) {
			return nil, apierror.OrganizationInvitationNotUnique()
		}
		return nil, apierror.Unexpected(txErr)
	}

	response := make([]*serialize.OrganizationInvitationResponse, len(invitations))
	for i, invitation := range invitations {
		response[i] = serialize.OrganizationInvitation(invitation)
	}
	return response, nil
}

type ListParams struct {
	OrganizationID   string
	RequestingUserID string
	Statuses         []string
}

func (params ListParams) validate() apierror.Error {
	for _, status := range params.Statuses {
		if !constants.OrganizationInvitationStatuses.Contains(status) {
			return apierror.FormInvalidParameterValueWithAllowed(param.Status.Name, status, constants.OrganizationInvitationStatuses.Array())
		}
	}
	return nil
}

func (s *Service) List(ctx context.Context, params ListParams, paginationParams pagination.Params) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(); apiErr != nil {
		return nil, apiErr
	}

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionMembersManage, params.RequestingUserID); apiErr != nil {
		return nil, apiErr
	}

	invitations, apiErr := s.organizationsService.ListInvitations(ctx, s.db, env.Instance.ID, params.OrganizationID, params.Statuses, paginationParams)
	if apiErr != nil {
		return nil, apiErr
	}

	response := make([]interface{}, len(invitations))
	for i, invitation := range invitations {
		response[i] = serialize.OrganizationInvitation(invitation)
	}

	count, err := s.organizationInvitationRepo.CountNonOrgDomainByOrganizationAndStatus(ctx, s.db, params.OrganizationID, params.Statuses)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Paginated(response, count), nil
}

// ListPendingInvitationsParams holds the organization ID, user ID and
// pagination options for listing an organization's pending invitations.
type ListPendingInvitationsParams struct {
	OrganizationID   string
	RequestingUserID string
	Paginated        *bool
}

// ListPendingInvitations retrieves a list of pending invitations for the provided organization ID.
// Any invitations created as a result of an Organization Domain, are filtered out. The reason is
// that we want to prevent users, that maybe don't have the proper permissions, to list users
// with the matching Organization Domain name.
// Results will be limited by params.Limit and params.Offset.
// Only organization admins can retrieve the list of invitations.
func (s *Service) ListPendingInvitations(
	ctx context.Context,
	params ListPendingInvitationsParams,
	paginationParams pagination.Params,
) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionMembersManage, params.RequestingUserID); apiErr != nil {
		return nil, apiErr
	}

	pendingInvitations, apiErr := s.organizationsService.ListInvitations(ctx, s.db, env.Instance.ID, params.OrganizationID, []string{constants.StatusPending}, paginationParams)
	if apiErr != nil {
		return nil, apiErr
	}

	response := make([]interface{}, len(pendingInvitations))
	for i, invitation := range pendingInvitations {
		response[i] = serialize.OrganizationInvitation(invitation)
	}

	if params.Paginated != nil && *params.Paginated {
		count, err := s.organizationInvitationRepo.CountPendingNonOrgDomainByOrganization(ctx, s.db, params.OrganizationID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		return serialize.Paginated(response, count), nil
	}

	return response, nil
}

type RevokeInvitationParams struct {
	organizations.RevokeInvitationParams
}

// RevokeInvitation attempts to change an invitation's status to revoked.
// The invitation is fetched by params.InvitationID and needs to be pending.
// The requesting user must be an admin in the organization specified by
// params.OrganizationID.
func (s *Service) RevokeInvitation(
	ctx context.Context,
	params RevokeInvitationParams,
) (*serialize.OrganizationInvitationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	var invitation *model.OrganizationInvitationSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		invitation, err = s.organizationsService.RevokeInvitation(ctx, tx, params.RevokeInvitationParams, env.Instance)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationInvitation(invitation), nil
}
