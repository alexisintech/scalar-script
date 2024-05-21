package organization_invitations

import (
	"context"
	"encoding/json"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/organizations"
	"clerk/api/shared/pagination"
	"clerk/model"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/set"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
)

type Service struct {
	db        database.Database
	validator *validator.Validate

	// services
	organizationsService *organizations.Service

	// repositories
	organizationsRepo           *repository.Organization
	organizationInvitationsRepo *repository.OrganizationInvitation
	userRepo                    *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                          deps.DB(),
		validator:                   validator.New(),
		organizationsService:        organizations.NewService(deps),
		organizationsRepo:           repository.NewOrganization(),
		organizationInvitationsRepo: repository.NewOrganizationInvitation(),
		userRepo:                    repository.NewUsers(),
	}
}

type CreateParams struct {
	EmailAddress    string           `json:"email_address" form:"email_address"`
	Role            string           `json:"role" form:"role"`
	RedirectURL     *string          `json:"redirect_url" form:"redirect_url"`
	InviterUserID   string           `json:"inviter_user_id" form:"inviter_user_id" validate:"required"`
	PublicMetadata  *json.RawMessage `json:"public_metadata" form:"public_metadata"`
	PrivateMetadata *json.RawMessage `json:"private_metadata" form:"private_metadata"`
}

func (p *CreateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, organizationID string, params CreateParams) (*serialize.OrganizationInvitationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	sharedParams, apiErr := s.toSharedParams(ctx, organizationID, env.Instance.ID, params)
	if apiErr != nil {
		return nil, apiErr
	}

	var invitation *model.OrganizationInvitationSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		invitations, err := s.organizationsService.CreateAndSendInvitations(ctx, tx, sharedParams, organizationID, env)
		if err != nil {
			return true, err
		}

		invitation = invitations[0]
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

	return serialize.OrganizationInvitationBAPI(invitation), nil
}

func (s *Service) toSharedParams(ctx context.Context, organizationID, instanceID string, params ...CreateParams) ([]organizations.CreateInvitationParams, apierror.Error) {
	organization, err := s.organizationsRepo.QueryByIDAndInstance(ctx, s.db, organizationID, instanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if organization == nil {
		return nil, apierror.ResourceNotFound()
	}

	inviterUserIDs := set.New[string]()
	for _, param := range params {
		inviterUserIDs.Insert(param.InviterUserID)
	}

	// Make sure all inviter users are organization administrators.
	users, err := s.userRepo.FindAllByInstanceAndIDs(ctx, s.db, instanceID, inviterUserIDs.Array())
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	sharedCreateParams := make([]organizations.CreateInvitationParams, len(params))
	for i, p := range params {
		sharedCreateParams[i] = organizations.CreateInvitationParams{
			OrganizationName: organization.Name,
			EmailAddress:     p.EmailAddress,
			PublicMetadata:   p.PublicMetadata,
			PrivateMetadata:  p.PrivateMetadata,
			Role:             p.Role,
			RedirectURL:      p.RedirectURL,
			InviterID:        p.InviterUserID,
		}
		for _, user := range users {
			if p.InviterUserID != user.ID {
				continue
			}
			sharedCreateParams[i].InviterName = user.Name()
		}
	}
	return sharedCreateParams, nil
}

func (s *Service) CreateBulk(ctx context.Context, organizationID string, params []CreateParams) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	sharedParams, apiErr := s.toSharedParams(ctx, organizationID, env.Instance.ID, params...)
	if apiErr != nil {
		return nil, apiErr
	}

	var invitations []*model.OrganizationInvitationSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		invitations, err = s.organizationsService.CreateAndSendInvitations(ctx, tx, sharedParams, organizationID, env)
		return err != nil, err
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

	paginated := make([]any, len(invitations))
	for i, invitation := range invitations {
		paginated[i] = serialize.OrganizationInvitationBAPI(invitation)
	}
	return serialize.Paginated(paginated, int64(len(paginated))), nil
}

type ListParams struct {
	OrganizationID string   `json:"-" form:"-"`
	Statuses       []string `json:"-" form:"-"`
}

func (p ListParams) validate() apierror.Error {
	for _, status := range p.Statuses {
		if !constants.OrganizationInvitationStatuses.Contains(status) {
			return apierror.FormInvalidParameterValueWithAllowed("status", status, constants.OrganizationInvitationStatuses.Array())
		}
	}
	return nil
}

func (s *Service) List(ctx context.Context, params ListParams, paginationParams pagination.Params) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(); apiErr != nil {
		return nil, apiErr
	}

	invitations, apiErr := s.organizationsService.ListInvitations(ctx, s.db, env.Instance.ID, params.OrganizationID, params.Statuses, paginationParams)
	if apiErr != nil {
		return nil, apiErr
	}

	totalCount, err := s.organizationInvitationsRepo.CountNonOrgDomainByOrganizationAndStatus(ctx, s.db, params.OrganizationID, params.Statuses)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responseData := make([]interface{}, len(invitations))
	for i, invitation := range invitations {
		responseData[i] = serialize.OrganizationInvitationBAPI(invitation)
	}
	return serialize.Paginated(responseData, totalCount), nil
}

func (s *Service) Read(ctx context.Context, orgID, invitationID string) (*serialize.OrganizationInvitationResponse, apierror.Error) {
	invitation, err := s.organizationsService.ReadInvitation(ctx, s.db, orgID, invitationID)
	if err != nil {
		return nil, err
	}

	return serialize.OrganizationInvitationBAPI(invitation), nil
}

type RevokeParams struct {
	RequestingUserID string `json:"requesting_user_id" form:"requesting_user_id" validate:"required"`
	OrganizationID   string `json:"-"`
	InvitationID     string `json:"-"`
}

func (p *RevokeParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

func (s *Service) Revoke(ctx context.Context, params RevokeParams) (*serialize.OrganizationInvitationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if err := params.validate(s.validator); err != nil {
		return nil, err
	}

	var invitation *model.OrganizationInvitationSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		invitation, err = s.organizationsService.RevokeInvitation(
			ctx,
			tx,
			organizations.RevokeInvitationParams{
				OrganizationID:   params.OrganizationID,
				InvitationID:     params.InvitationID,
				RequestingUserID: params.RequestingUserID,
			},
			env.Instance,
		)
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationInvitationBAPI(invitation), nil
}
