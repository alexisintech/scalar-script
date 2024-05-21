package allowlist

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/comms"
	"clerk/api/shared/invitations"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/emailaddress"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/go-playground/validator/v10"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	db        database.Database
	validator *validator.Validate

	// services
	comms       *comms.Service
	invitations *invitations.Service

	// repositories
	allowlistRepo  *repository.Allowlist
	invitationRepo *repository.Invitations
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:             deps.DB(),
		validator:      validator.New(),
		comms:          comms.NewService(deps),
		invitations:    invitations.NewService(deps),
		allowlistRepo:  repository.NewAllowlist(),
		invitationRepo: repository.NewInvitations(),
	}
}

// ReadAllPaginated returns all allowlist identifiers for the instance.
// The response is a serialize.PaginatedResponse, returning the list
// and the total count.
func (s *Service) ReadAllPaginated(ctx context.Context) (*serialize.PaginatedResponse, apierror.Error) {
	list, apiErr := s.ReadAll(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	totalCount := len(list)
	data := make([]any, totalCount)
	for i, allowlistIdentifier := range list {
		data[i] = allowlistIdentifier
	}
	return serialize.Paginated(data, int64(totalCount)), nil
}

// ReadAll returns all identifiers in the allowlist of the instance.
func (s *Service) ReadAll(ctx context.Context) ([]*serialize.AllowlistIdentifierResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	identifiers, err := s.allowlistRepo.FindAllByInstance(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	list := make([]*serialize.AllowlistIdentifierResponse, len(identifiers))
	for i, identifier := range identifiers {
		list[i] = serialize.AllowlistIdentifier(identifier)
	}

	return list, nil
}

// CreateParams is the user-provided params of AllowlistIdentifier
type CreateParams struct {
	Identifier string `json:"identifier" form:"identifier" validate:"required"`
	Notify     bool   `json:"notify" form:"notify"`
}

// Create creates a new allowed identifier for the instance, and optionally sends a notification
func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.AllowlistIdentifierResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if err := s.validator.Struct(params); err != nil {
		return nil, apierror.FormValidationFailed(err)
	}

	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	identifierAttribute := userSettings.IdentifierToAttribute(
		params.Identifier,
		names.EmailAddress,
		names.PhoneNumber,
		names.Web3Wallet,
	)
	if identifierAttribute == nil {
		return nil, apierror.FormInvalidIdentifier("identifier")
	}

	var apiErr apierror.Error
	params.Identifier, apiErr = identifierAttribute.Sanitize(params.Identifier, "identifier")
	if apiErr != nil {
		return nil, apiErr
	}

	if params.Notify {
		if emailaddress.IsDomainWhitelist(params.Identifier) || validate.Web3Wallet(params.Identifier, "") == nil {
			return nil, apierror.FormParameterNotAllowedConditionally(param.AllowlistNotify.Name, param.AllowlistIdentifier.Name, "an email domain or a web3 wallet")
		}
	}

	identifier := &model.AllowlistIdentifier{
		AllowlistIdentifier: &sqbmodel.AllowlistIdentifier{
			Identifier:     params.Identifier,
			IdentifierType: identifierAttribute.Name(),
			InstanceID:     env.Instance.ID,
		},
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err := s.allowlistRepo.Insert(ctx, tx, identifier); err != nil {
			return true, fmt.Errorf("allowlistRepo: inserting identifier %+v: %w", identifier, err)
		}

		if params.Notify {
			accountsURL := env.Domain.AccountsURL()
			actionURL := env.DisplayConfig.Paths.SignUpURL(env.Instance.Origin(env.Domain, nil), accountsURL)

			if identifierAttribute.Name() == constants.ITEmailAddress {
				invitation, err := s.invitationRepo.QueryByNonRevokedEmailAddressAndInstance(ctx, tx, params.Identifier, env.Instance.ID)
				if err != nil {
					return true, fmt.Errorf("allowlist/create: find existing invitation by %s, %s", params.Identifier, env.Instance.ID)
				}
				if invitation == nil {
					invitation, err = s.invitations.Create(ctx, tx, env, invitations.CreateInvitationForm{
						EmailAddress: params.Identifier,
					})
					if err != nil {
						return true, fmt.Errorf("allowlist/create: creating invitation for %s on instance %s: %w",
							params.Identifier, env.Instance.ID, err)
					}
				}

				invitationURL, err := s.invitations.CreateLink(invitation, env, &actionURL)
				if err != nil {
					return true, err
				}

				err = s.comms.SendInvitationEmail(ctx, tx, env, invitation, invitationURL)
				if err != nil {
					return true, fmt.Errorf("allowlist/create: sending invitation for %s, %s: %w", params.Identifier, env.Instance.ID, err)
				}

				identifier.InvitationID = null.StringFrom(invitation.ID)
				if err := s.allowlistRepo.UpdateInvitationID(ctx, tx, identifier); err != nil {
					return true, fmt.Errorf("allowlist/create: adding invitation id %s on allowlist identifier %s: %w",
						invitation.ID, identifier.ID, err)
				}
			} else {
				if err := s.comms.SendInvitationSMS(ctx, tx, env, identifier.Identifier, actionURL); err != nil {
					return true, fmt.Errorf("allowlist/create: sending SMS invitation for instance %s to %s: %w",
						env.Instance.ID, identifier.Identifier, err)
				}
			}
		}

		return false, nil
	})
	if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueAllowlistIdentifierForInstance) {
		return nil, apierror.DuplicateAllowlistIdentifier(params.Identifier)
	} else if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.AllowlistIdentifier(identifier), nil
}

// Delete removes an identifier from the instance allowlist
func (s *Service) Delete(ctx context.Context, identifierID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	identifier, err := s.allowlistRepo.QueryByInstanceAndID(ctx, s.db, env.Instance.ID, identifierID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if identifier == nil {
		return nil, apierror.AllowlistIdentifierNotFound(identifierID)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err = s.allowlistRepo.DeleteByID(ctx, tx, identifier.ID)
		if err != nil {
			return true, err
		}

		invitation, err := s.invitationRepo.QueryByNonRevokedEmailAddressAndInstance(ctx, tx, identifier.Identifier, identifier.InstanceID)
		if err != nil {
			return true, err
		}

		if invitation != nil {
			invitation.Status = constants.StatusRevoked
			if err := s.invitationRepo.UpdateStatus(ctx, tx, invitation); err != nil {
				return true, err
			}
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.DeletedObject(identifierID, serialize.AllowlistIdentifierObjectName), nil
}
