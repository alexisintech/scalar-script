package organization_memberships

import (
	"context"
	"encoding/json"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/organizations"
	"clerk/api/shared/orgdomain"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/metadata"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	// services
	eventsService        *events.Service
	organizationsService *organizations.Service
	orgDomainService     *orgdomain.Service

	// repositories
	organizationMembershipsRepo *repository.OrganizationMembership
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                          deps.DB(),
		eventsService:               events.NewService(deps),
		organizationsService:        organizations.NewService(deps),
		orgDomainService:            orgdomain.NewService(deps.Clock()),
		organizationMembershipsRepo: repository.NewOrganizationMembership(),
	}
}

var validOrderByFields = set.New(
	sqbmodel.OrganizationMembershipColumns.CreatedAt,
	repository.OrderFieldEmailAddress,
	repository.OrderFieldFirstName,
	repository.OrderFieldLastName,
	repository.OrderFieldUsername,
	repository.OrderFieldPhoneNumber,
	repository.OrderFieldRole,
)

type ListParams struct {
	OrganizationID string
	orderBy        string
	repository.OrganizationMembershipsFindAllModifiers
}

func (params ListParams) convertToOrganizationMembershipMods() (repository.OrganizationMembershipsFindAllModifiers, apierror.Error) {
	var mods repository.OrganizationMembershipsFindAllModifiers
	mods.EmailAddresses = params.EmailAddresses
	mods.PhoneNumbers = params.PhoneNumbers
	mods.Usernames = params.Usernames
	mods.Web3Wallets = params.Web3Wallets
	mods.Query = params.Query
	mods.UserIDs = params.UserIDs
	mods.Roles = params.Roles

	if params.orderBy != "" {
		orderByField, err := repository.ConvertToOrderByField(params.orderBy, validOrderByFields)
		if err != nil {
			return mods, err
		}
		mods.OrderBy = &orderByField
	}
	return mods, nil
}

// List retrieves a list of all organization members for the
// organization specified by params.OrganizationID.
// The method support pagination on the results.
func (s *Service) List(ctx context.Context, params ListParams, paginationParams pagination.Params) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	mods, apiErr := params.convertToOrganizationMembershipMods()
	if apiErr != nil {
		return nil, apiErr
	}

	membershipsResponse, err := s.organizationMembershipsRepo.FindAllByOrganizationWithModifiers(ctx, s.db, env.Instance.ID, params.OrganizationID, mods, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalCount, err := s.organizationMembershipsRepo.CountByOrganizationWithModifiers(ctx, s.db, env.Instance.ID, params.OrganizationID, mods)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responseData := make([]interface{}, len(membershipsResponse))
	for i, orgMembership := range membershipsResponse {
		membership, err := s.organizationsService.ConvertToSerializable(ctx, s.db, orgMembership)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		responseData[i] = serialize.OrganizationMembershipBAPI(ctx, membership)
	}

	return serialize.Paginated(responseData, totalCount), apiErr
}

type CreateParams struct {
	OrganizationID string
	UserID         string `json:"user_id" form:"user_id"`
	Role           string `json:"role" form:"role"`
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.OrganizationMembershipResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	var membership *model.OrganizationMembershipSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		membership, err = s.organizationsService.CreateMembership(ctx, tx, organizations.CreateMembershipParams{
			OrganizationID: params.OrganizationID,
			UserID:         params.UserID,
			Role:           params.Role,
			Instance:       env.Instance,
			Subscription:   env.Subscription,
		})
		if err != nil {
			return true, err
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
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationMembershipBAPI(ctx, membership), nil
}

type UpdateParams struct {
	OrganizationID string
	UserID         string
	Role           string `json:"role" form:"role"`
}

func (s *Service) Update(ctx context.Context, params UpdateParams) (*serialize.OrganizationMembershipResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	var membership *model.OrganizationMembershipSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		membership, err = s.organizationsService.UpdateMembership(ctx, tx, organizations.UpdateMembershipParams{
			OrganizationID: params.OrganizationID,
			UserID:         params.UserID,
			Role:           params.Role,
			Instance:       env.Instance,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationMembershipBAPI(ctx, membership), nil
}

type UpdateMetadataParams struct {
	PublicMetadata  json.RawMessage `json:"public_metadata" form:"public_metadata"`
	PrivateMetadata json.RawMessage `json:"private_metadata" form:"private_metadata"`
	OrganizationID  string          `json:"-"`
	UserID          string          `json:"-"`
}

func (s *Service) UpdateMetadata(ctx context.Context, params UpdateMetadataParams) (*serialize.OrganizationMembershipResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	membership, err := s.organizationMembershipsRepo.QueryByOrganizationAndUser(ctx, s.db, params.OrganizationID, params.UserID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if membership == nil {
		return nil, apierror.ResourceNotFound()
	}
	orgMembership := &membership.OrganizationMembership

	merged, mergeErr := metadata.Merge(orgMembership.Metadata(), metadata.Metadata{
		Public:  params.PublicMetadata,
		Private: params.PrivateMetadata,
	})
	if mergeErr != nil {
		return nil, mergeErr
	}
	orgMembership.SetMetadata(merged)

	var serializable *model.OrganizationMembershipSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.organizationMembershipsRepo.UpdateMetadata(ctx, tx, orgMembership)
		if err != nil {
			return true, err
		}
		serializable, err = s.organizationsService.ConvertToSerializable(ctx, tx, membership)
		if err != nil {
			return true, err
		}

		eventPayload := serialize.OrganizationMembership(ctx, serializable)
		err = s.eventsService.OrganizationMembershipUpdated(ctx, tx, env.Instance, eventPayload, params.OrganizationID, params.UserID)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationMembershipBAPI(ctx, serializable), nil
}

func (s *Service) Delete(ctx context.Context, organizationID, userID string) (*serialize.OrganizationMembershipResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	var membership *model.OrganizationMembershipSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		membership, err = s.organizationsService.DeleteMembership(ctx, organizations.DeleteMembershipParams{
			OrganizationID: organizationID,
			UserID:         userID,
			Env:            env,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationMembershipBAPI(ctx, membership), nil
}
