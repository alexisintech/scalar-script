package serialize

import (
	"encoding/json"

	"clerk/model"
	"clerk/pkg/time"
)

const InvitationObjectName = "invitation"

type InvitationResponse struct {
	Object         string          `json:"object"`
	ID             string          `json:"id"`
	EmailAddress   string          `json:"email_address"`
	PublicMetadata json.RawMessage `json:"public_metadata" logger:"omit"`
	Revoked        bool            `json:"revoked,omitempty"`
	Status         string          `json:"status"`
	URL            string          `json:"url,omitempty"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`
}

func WithInvitationURL(invitationURL string) func(*InvitationResponse) {
	return func(invitationResponse *InvitationResponse) {
		invitationResponse.URL = invitationURL
	}
}

func Invitation(invitation *model.Invitation, opts ...func(*InvitationResponse)) *InvitationResponse {
	response := &InvitationResponse{
		Object:         InvitationObjectName,
		ID:             invitation.ID,
		EmailAddress:   invitation.EmailAddress,
		PublicMetadata: json.RawMessage(invitation.PublicMetadata),
		Status:         invitation.Status,
		Revoked:        invitation.IsRevoked(),
		CreatedAt:      time.UnixMilli(invitation.CreatedAt),
		UpdatedAt:      time.UnixMilli(invitation.UpdatedAt),
	}
	for _, opt := range opts {
		opt(response)
	}
	return response
}
