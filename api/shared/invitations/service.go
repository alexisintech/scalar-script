package invitations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ticket"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/sqlboiler/v4/types"
)

type Service struct {
	clock clockwork.Clock

	// repositories
	invitationsRepo *repository.Invitations
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:           deps.Clock(),
		invitationsRepo: repository.NewInvitations(),
	}
}

type CreateInvitationForm struct {
	EmailAddress   string
	PublicMetadata *json.RawMessage
	RedirectURL    *url.URL
}

// Create creates a new entry in the invitations table.
func (s *Service) Create(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	createForm CreateInvitationForm,
) (*model.Invitation, error) {
	invitation := &model.Invitation{
		Invitation: &sqbmodel.Invitation{
			InstanceID:   env.Instance.ID,
			EmailAddress: createForm.EmailAddress,
		},
	}

	if createForm.PublicMetadata != nil {
		invitation.PublicMetadata = types.JSON(*createForm.PublicMetadata)
	}

	if err := s.invitationsRepo.Insert(ctx, tx, invitation); err != nil {
		return invitation, fmt.Errorf("invitations/create: inserting invitation %+v: %w", invitation, err)
	}
	return invitation, nil
}

func createInvitationLink(ticket, fapiURL string) (string, error) {
	link, err := url.Parse(fapiURL)
	if err != nil {
		return "", err
	}

	link = link.JoinPath("/v1/tickets/accept")
	query := link.Query()
	query.Set("ticket", ticket)
	link.RawQuery = query.Encode()
	return link.String(), nil
}

// CreateLink generates the invitation token and return the url to be visited.
func (s *Service) CreateLink(invitation *model.Invitation, env *model.Env, redirectURL *string) (string, error) {
	claims := ticket.Claims{
		InstanceID:  invitation.InstanceID,
		SourceType:  constants.OSTInvitation,
		SourceID:    invitation.ID,
		RedirectURL: redirectURL,
	}
	token, err := ticket.Generate(claims, env.Instance, s.clock)
	if err != nil {
		return "", fmt.Errorf("cannot generate invitation token for %+v: %w", invitation, err)
	}

	fapiURL := env.Domain.FapiURL()
	actionURL, err := createInvitationLink(token, fapiURL)
	if err != nil {
		return "", err
	}

	return actionURL, nil
}
