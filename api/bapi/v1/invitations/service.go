package invitations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/comms"
	"clerk/api/shared/invitations"
	"clerk/api/shared/pagination"
	"clerk/api/shared/validators"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/metadata"
	"clerk/pkg/set"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/go-playground/validator/v10"
)

type Service struct {
	db        database.Database
	validator *validator.Validate

	// services
	comms       *comms.Service
	invitations *invitations.Service
	validators  *validators.Service

	// repositories
	invitationsRepo *repository.Invitations
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:              deps.DB(),
		validator:       validator.New(),
		comms:           comms.NewService(deps),
		invitations:     invitations.NewService(deps),
		validators:      validators.NewService(),
		invitationsRepo: repository.NewInvitations(),
	}
}

type CreateParams struct {
	EmailAddress   string           `json:"email_address" form:"email_address" validate:"required"`
	PublicMetadata *json.RawMessage `json:"public_metadata" form:"public_metadata"`
	RedirectURL    *string          `json:"redirect_url" form:"redirect_url"`
	Notify         *bool            `json:"notify" form:"notify"`
	IgnoreExisting *bool            `json:"ignore_existing" form:"ignore_existing"`
}

func (p CreateParams) validate(validator *validator.Validate, userSettings *usersettings.UserSettings) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	var apiErrors apierror.Error

	if !userSettings.IsEnabled(names.EmailAddress) {
		return apierror.InvitationsNotSupportedInInstance()
	}

	if err := validate.EmailAddress(p.EmailAddress, param.EmailAddress.Name); err != nil {
		apiErrors = apierror.Combine(apiErrors, err)
	}

	if p.RedirectURL != nil {
		if _, err := url.ParseRequestURI(*p.RedirectURL); err != nil {
			apiErrors = apierror.Combine(apiErrors, apierror.FormInvalidTypeParameter(param.RedirectURL.Name, "valid url"))
		}
	}

	apiErrors = apierror.Combine(apiErrors, metadata.Validate(p.toMetadata()))

	return apiErrors
}

func (p CreateParams) toMetadata() metadata.Metadata {
	v := metadata.Metadata{}
	if p.PublicMetadata != nil {
		v.Public = *p.PublicMetadata
	}
	return v
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.InvitationResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	apiErr := params.validate(s.validator, userSettings)
	if apiErr != nil {
		return nil, apiErr
	}

	// sanitize email address
	params.EmailAddress, apiErr = userSettings.GetAttribute(names.EmailAddress).Sanitize(params.EmailAddress, "email_address")
	if apiErr != nil {
		return nil, apiErr
	}

	if params.IgnoreExisting == nil || !*params.IgnoreExisting {
		apiErr := s.checkIfInvitationCanBeCreated(ctx, params.EmailAddress, env.Instance.ID, userSettings)
		if apiErr != nil {
			return nil, apiErr
		}
	}

	var redirectURL *url.URL
	var err error
	if params.RedirectURL != nil {
		if redirectURL, err = url.Parse(*params.RedirectURL); err != nil {
			// return unexpected here because we've already checked that the redirect url is valid
			return nil, apierror.Unexpected(err)
		}
	}

	var invitation *model.Invitation
	var invitationLink string
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		invitation, err = s.invitations.Create(ctx, tx, env, invitations.CreateInvitationForm{
			EmailAddress:   params.EmailAddress,
			PublicMetadata: params.PublicMetadata,
			RedirectURL:    redirectURL,
		})
		if err != nil {
			return true, err
		}

		invitationLink, err = s.invitations.CreateLink(invitation, env, params.RedirectURL)
		if err != nil {
			return true, err
		}

		// If the `notify` flag is not given or if it's given as `true`
		// we send the invitation email.
		// The null check was added to maintain backwards compatibility.
		if params.Notify == nil || *params.Notify {
			if err := s.comms.SendInvitationEmail(ctx, tx, env, invitation, invitationLink); err != nil {
				return true, fmt.Errorf("cannot send invitation email to %s: %w", invitation.EmailAddress, err)
			}
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.Invitation(invitation, serialize.WithInvitationURL(invitationLink)), nil
}

func (s *Service) checkIfInvitationCanBeCreated(ctx context.Context, emailAddress, instanceID string, userSettings *usersettings.UserSettings) apierror.Error {
	emailAddressCanBeReserved := !userSettings.GetAttribute(names.EmailAddress).Base().VerifyAtSignUp
	isUnique, err := s.validators.IsUniqueIdentifier(ctx, s.db, emailAddress, constants.ITEmailAddress, instanceID, emailAddressCanBeReserved)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if !isUnique {
		return apierror.FormIdentifierExists(param.EmailAddress.Name)
	}

	invitation, err := s.invitationsRepo.QueryByNonRevokedEmailAddressAndInstance(ctx, s.db, emailAddress, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if invitation != nil {
		return apierror.DuplicateInvitations(emailAddress)
	}
	return nil
}

type ReadAllParams struct {
	Statuses   []string
	Pagination *pagination.Params
}

func (params ReadAllParams) validate() apierror.Error {
	validStatuses := set.New(
		constants.StatusPending,
		constants.StatusAccepted,
		constants.StatusRevoked,
	)
	invalidStatuses := set.New(params.Statuses...)
	invalidStatuses.Subtract(validStatuses)
	if len(params.Statuses) > 0 && invalidStatuses.Count() > 0 {
		return apierror.FormInvalidParameterValueWithAllowed("status", strings.Join(invalidStatuses.Array(), ", "), validStatuses.Array())
	}
	return nil
}

// ReadAllPaginated returns all instance invitations with the provided params, but the
// response schema follows the paginated responses.
// Please note that the method does not actually support pagination, it just returns
// the results in a serialize.PaginatedResponse.
func (s *Service) ReadAllPaginated(ctx context.Context, params ReadAllParams) (*serialize.PaginatedResponse, apierror.Error) {
	list, apiErr := s.ReadAll(ctx, params)
	if apiErr != nil {
		return nil, apiErr
	}
	totalCount := len(list)
	data := make([]any, totalCount)
	for i, invitation := range list {
		data[i] = invitation
	}
	return serialize.Paginated(data, int64(totalCount)), nil
}

// ReadAll returns all instance invitations with the provided params.
func (s *Service) ReadAll(ctx context.Context, params ReadAllParams) ([]*serialize.InvitationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := params.validate()
	if apiErr != nil {
		return nil, apiErr
	}

	statuses := params.Statuses
	if len(statuses) == 0 {
		// if no status supplied, return all non-revoked (for backwards compatibility)
		statuses = append(statuses, constants.StatusPending, constants.StatusAccepted)
	}

	allInvitations, err := s.invitationsRepo.FindAllByStatusesAndInstanceAndPagination(ctx, s.db, statuses, env.Instance.ID, params.Pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]*serialize.InvitationResponse, len(allInvitations))
	for i, invitation := range allInvitations {
		response[i] = serialize.Invitation(invitation)
	}
	return response, nil
}

func (s *Service) Revoke(ctx context.Context, invitationID string) (*serialize.InvitationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	invitation, err := s.invitationsRepo.QueryByIDAndInstance(ctx, s.db, invitationID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if invitation == nil {
		return nil, apierror.InvitationNotFound(invitationID)
	}

	if invitation.IsAccepted() {
		return nil, apierror.InvitationAlreadyAccepted()
	}
	if invitation.IsRevoked() {
		return nil, apierror.InvitationAlreadyRevoked()
	}

	invitation.Status = constants.StatusRevoked
	if err := s.invitationsRepo.UpdateStatus(ctx, s.db, invitation); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.Invitation(invitation), nil
}
