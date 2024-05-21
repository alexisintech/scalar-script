package organization_memberships

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/organizations"
	"clerk/api/shared/orgdomain"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	// services
	organizationsService *organizations.Service
	orgDomainService     *orgdomain.Service

	// repositories
	orgMembershipRepo *repository.OrganizationMembership
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                   deps.DB(),
		organizationsService: organizations.NewService(deps),
		orgDomainService:     orgdomain.NewService(deps.Clock()),
		orgMembershipRepo:    repository.NewOrganizationMembership(),
	}
}

type CreateMembershipParams struct {
	OrganizationID   string
	UserID           string
	Role             string
	RequestingUserID string
}

// Create will try to create a new membership for an organization and respond with information about
// the membership and the member user.
// Only an organization admin can create a membership.
func (s *Service) Create(ctx context.Context, params *CreateMembershipParams) (*serialize.OrganizationMembershipResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionMembersManage, params.RequestingUserID)
	if apiErr != nil {
		return nil, apiErr
	}

	var membership *model.OrganizationMembershipSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		membership, apiErr = s.organizationsService.CreateMembership(ctx, tx, organizations.CreateMembershipParams{
			OrganizationID:   params.OrganizationID,
			UserID:           params.UserID,
			Role:             params.Role,
			RequestingUserID: params.RequestingUserID,
			Instance:         env.Instance,
			Subscription:     env.Subscription,
		})
		if apiErr != nil {
			return true, apiErr
		}

		// delete pending org domain invitations and suggestions for user
		if env.AuthConfig.IsOrganizationDomainsEnabled() {
			err := s.orgDomainService.DeletePendingInvitationsAndSuggestionsForUserAndOrg(ctx, tx, params.UserID, params.OrganizationID)
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
	}

	return serialize.OrganizationMembership(ctx, membership), nil
}

type ListMembershipsParams struct {
	RequestingUserID string
	OrganizationID   string
	Roles            []string
	Paginated        *bool
}

// List retrieves a list of all organization members for the
// organization specified by params.OrganizationID.
// The requesting user must be a member of the organization. The method
// support pagination on the results.
func (s *Service) List(ctx context.Context, params ListMembershipsParams, paginationParams pagination.Params) (interface{}, apierror.Error) {
	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionMembersRead, params.RequestingUserID); apiErr != nil {
		return nil, apiErr
	}

	memberships, apiErr := s.organizationsService.ListMemberships(ctx, s.db, organizations.ListMembershipsParams{
		OrganizationID: &params.OrganizationID,
		Roles:          params.Roles,
	}, paginationParams)
	if apiErr != nil {
		return nil, apiErr
	}

	response := make([]interface{}, len(memberships))
	for i, membership := range memberships {
		response[i] = serialize.OrganizationMembership(ctx, membership)
	}

	if params.Paginated != nil && *params.Paginated {
		count, err := s.orgMembershipRepo.CountByOrganizationAndRoles(ctx, s.db, params.OrganizationID, params.Roles)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		return serialize.Paginated(response, count), nil
	}

	return response, apiErr
}

type UpdateMembershipParams struct {
	OrganizationID   string
	UserID           string
	Role             string
	RequestingUserID string
}

// Update will try to update the role of an organization membership and respond with information
// about the membership and the member user.
func (s *Service) Update(ctx context.Context, params *UpdateMembershipParams) (*serialize.OrganizationMembershipResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	err := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionMembersManage, params.RequestingUserID)
	if err != nil {
		return nil, err
	}

	var membership *model.OrganizationMembershipSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		membership, err = s.organizationsService.UpdateMembership(ctx, tx, organizations.UpdateMembershipParams{
			OrganizationID:   params.OrganizationID,
			UserID:           params.UserID,
			Role:             params.Role,
			RequestingUserID: params.RequestingUserID,
			Instance:         env.Instance,
		})
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationMembership(ctx, membership), nil
}

type DeleteMembershipParams struct {
	OrganizationID   string
	UserID           string
	RequestingUserID string
}

// Delete removes the user with params.UserID from the provided organization.
func (s *Service) Delete(ctx context.Context, params DeleteMembershipParams) (*serialize.OrganizationMembershipResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := s.organizationsService.EnsureHasAccess(ctx, s.db, params.OrganizationID, constants.PermissionMembersManage, params.RequestingUserID); apiErr != nil {
		return nil, apiErr
	}

	var membership *model.OrganizationMembershipSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err apierror.Error
		membership, err = s.organizationsService.DeleteMembership(ctx, organizations.DeleteMembershipParams{
			OrganizationID:   params.OrganizationID,
			UserID:           params.UserID,
			RequestingUserID: params.RequestingUserID,
			Env:              env,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationMembership(ctx, membership), nil
}
