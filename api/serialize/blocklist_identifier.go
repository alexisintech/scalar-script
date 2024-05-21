package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const BlocklistIdentifierObjectName = "blocklist_identifier"

type BlocklistIdentifierResponse struct {
	Object         string `json:"object"`
	ID             string `json:"id"`
	Identifier     string `json:"identifier"`
	IdentifierType string `json:"identifier_type"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

func BlocklistIdentifier(identifier *model.BlocklistIdentifier) *BlocklistIdentifierResponse {
	return &BlocklistIdentifierResponse{
		Object:         BlocklistIdentifierObjectName,
		ID:             identifier.ID,
		Identifier:     identifier.Identifier,
		IdentifierType: identifier.IdentifierType,
		CreatedAt:      time.UnixMilli(identifier.CreatedAt),
		UpdatedAt:      time.UnixMilli(identifier.UpdatedAt),
	}
}
