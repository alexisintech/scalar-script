package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const AllowlistIdentifierObjectName = "allowlist_identifier"

type AllowlistIdentifierResponse struct {
	Object         string `json:"object"`
	ID             string `json:"id"`
	InvitationID   string `json:"invitation_id,omitempty"`
	Identifier     string `json:"identifier"`
	IdentifierType string `json:"identifier_type"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

func AllowlistIdentifier(identifier *model.AllowlistIdentifier) *AllowlistIdentifierResponse {
	return &AllowlistIdentifierResponse{
		Object:         AllowlistIdentifierObjectName,
		ID:             identifier.ID,
		Identifier:     identifier.Identifier,
		IdentifierType: identifier.IdentifierType,
		InvitationID:   identifier.InvitationID.String,
		CreatedAt:      time.UnixMilli(identifier.CreatedAt),
		UpdatedAt:      time.UnixMilli(identifier.UpdatedAt),
	}
}
