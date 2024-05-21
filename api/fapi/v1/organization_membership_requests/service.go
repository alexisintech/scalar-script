package organization_membership_requests

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/comms"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/api/shared/serializable"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	db database.Database

	// services
	commsService         *comms.Service
	organizationsService *organizations.Service
	serializableService  *serializable.Service

	// repositories
	organizationRepo     *repository.Organization
	orgMemberRequestRepo *repository.OrganizationMembershipRequest
	orgSuggestionRepo    *repository.OrganizationSuggestion
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                   deps.DB(),
		commsService:         comms.NewService(deps),
		organizationsService: organizations.NewService(deps),
		serializableService:  serializable.NewService(deps.Clock()),
		organizationRepo:     repository.NewOrganization(),
		orgMemberRequestRepo: repository.NewOrganizationMembershipRequest(),
		orgSuggestionRepo:    repository.NewOrganizationSuggestion(),
	}
}

type ListParams struct {
	OrganizationID string
	Statuses       []string
}

func (params ListParams) validate() apierror.Error {
	for _, status := range params.Statuses {
		if !constants.OrganizationMembershipRequestStatuses.Contains(status) {
			return apierror.FormInvalidParameterValueWithAllowed(param.Status.Name, status, constants.OrganizationMembershipRequestStatuses.Array())
		}
	}
	return nil
}

func (s *Service) List(ctx context.Context, params ListParams, paginationParams pagination.Params) (interface{}, apierror.Error) {
	if apiErr := params.validate(); apiErr != nil {
		return nil, apiErr
	}

	membershipRequests, err := s.orgMemberRequestRepo.FindAllByOrganizationAndStatus(ctx, s.db, params.OrganizationID, params.Statuses, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]interface{}, len(membershipRequests))
	for i, membershipRequest := range membershipRequests {
		membershipRequestSerializable, err := s.serializableService.ConvertOrganizationMembershipRequest(ctx, s.db, membershipRequest)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		response[i] = serialize.OrganizationMembershipRequest(membershipRequestSerializable)
	}

	count, err := s.orgMemberRequestRepo.CountByOrganizationAndStatus(ctx, s.db, params.OrganizationID, params.Statuses)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Paginated(response, count), nil
}

type AcceptParams struct {
	OrganizationID   string
	RequestID        string
	RequestingUserID string
}

func (s *Service) Accept(ctx context.Context, params AcceptParams) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)

	membershipRequest, err := s.orgMemberRequestRepo.QueryPendingByOrganizationAndID(ctx, s.db, params.OrganizationID, params.RequestID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if membershipRequest == nil {
		return nil, apierror.ResourceNotFound()
	}

	organization, err := s.organizationRepo.QueryByIDAndInstance(ctx, s.db, params.OrganizationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if organization == nil {
		return nil, apierror.ResourceNotFound()
	}

	var memberRequestSerializable *model.OrganizationMembershipRequestSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		membership, apiErr := s.organizationsService.CreateMembership(ctx, tx, organizations.CreateMembershipParams{
			OrganizationID:   params.OrganizationID,
			UserID:           membershipRequest.UserID,
			Role:             env.AuthConfig.OrganizationSettings.Domains.DefaultRole,
			RequestingUserID: params.RequestingUserID,
			Instance:         env.Instance,
			Subscription:     env.Subscription,
		})
		if apiErr != nil {
			return true, apiErr
		}

		membershipRequest.ApprovedBy = null.StringFrom(params.RequestingUserID)
		membershipRequest.OrganizationMembershipID = null.StringFrom(membership.OrganizationMembership.ID)
		membershipRequest.Status = constants.StatusAccepted
		if err := s.orgMemberRequestRepo.Update(ctx, tx, membershipRequest,
			sqbmodel.OrganizationMembershipRequestColumns.ApprovedBy,
			sqbmodel.OrganizationMembershipRequestColumns.OrganizationMembershipID,
			sqbmodel.OrganizationMembershipRequestColumns.Status,
		); err != nil {
			return true, err
		}

		// update corresponding organization suggestion status to `completed`
		suggestion, apiErr := s.updateAcceptedOrganizationSuggestionStatus(ctx, tx, membershipRequest.OrganizationSuggestionID, constants.StatusCompleted)
		if apiErr != nil {
			return true, apiErr
		}

		emailParams := comms.EmailOrganizationJoined{
			Organization: organization,
			EmailAddress: suggestion.EmailAddress,
		}
		if err = s.commsService.SendOrganizationJoinedEmail(ctx, tx, env, emailParams); err != nil {
			return true, fmt.Errorf("prepare: sending organization joined email failed: %w", err)
		}

		memberRequestSerializable, err = s.serializableService.ConvertOrganizationMembershipRequest(ctx, tx, membershipRequest)
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

	return serialize.OrganizationMembershipRequest(memberRequestSerializable), nil
}

func (s *Service) Reject(ctx context.Context, params AcceptParams) (interface{}, apierror.Error) {
	membershipRequest, err := s.orgMemberRequestRepo.QueryPendingByOrganizationAndID(ctx, s.db, params.OrganizationID, params.RequestID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if membershipRequest == nil {
		return nil, apierror.ResourceNotFound()
	}

	var memberRequestSerializable *model.OrganizationMembershipRequestSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// update membership request status to `rejected`
		membershipRequest.Status = constants.StatusRejected
		if err := s.orgMemberRequestRepo.UpdateStatus(ctx, tx, membershipRequest); err != nil {
			return true, err
		}

		// update corresponding organization suggestion status to `completed`
		_, apiErr := s.updateAcceptedOrganizationSuggestionStatus(ctx, tx, membershipRequest.OrganizationSuggestionID, constants.StatusCompleted)
		if apiErr != nil {
			return true, apiErr
		}

		memberRequestSerializable, err = s.serializableService.ConvertOrganizationMembershipRequest(ctx, tx, membershipRequest)
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

	return serialize.OrganizationMembershipRequest(memberRequestSerializable), nil
}

func (s *Service) updateAcceptedOrganizationSuggestionStatus(ctx context.Context, tx database.Tx, organizationSuggestionID, newStatus string) (*model.OrganizationSuggestion, apierror.Error) {
	suggestion, err := s.orgSuggestionRepo.QueryAcceptedByID(ctx, tx, organizationSuggestionID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if suggestion == nil {
		return nil, apierror.ResourceNotFound()
	}

	suggestion.Status = newStatus
	err = s.orgSuggestionRepo.UpdateStatus(ctx, tx, suggestion)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return suggestion, nil
}
