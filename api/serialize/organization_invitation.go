package serialize

import (
	"context"
	"encoding/json"

	"clerk/model"
	"clerk/pkg/time"
)

const OrganizationInvitationObjectName = "organization_invitation"

type OrganizationInvitationResponse struct {
	Object                 string                          `json:"object"`
	ID                     string                          `json:"id"`
	EmailAddress           string                          `json:"email_address"`
	Role                   string                          `json:"role"`
	OrganizationID         string                          `json:"organization_id,omitempty"`
	PublicOrganizationData *publicOrganizationDataResponse `json:"public_organization_data,omitempty" logger:"omit"`
	Status                 string                          `json:"status,omitempty"`
	PublicMetadata         json.RawMessage                 `json:"public_metadata" logger:"omit"`
	PrivateMetadata        json.RawMessage                 `json:"private_metadata,omitempty" logger:"omit"`
	CreatedAt              int64                           `json:"created_at"`
	UpdatedAt              int64                           `json:"updated_at"`
}

func OrganizationInvitation(invitation *model.OrganizationInvitationSerializable) *OrganizationInvitationResponse {
	response := &OrganizationInvitationResponse{
		Object:         OrganizationInvitationObjectName,
		ID:             invitation.ID,
		EmailAddress:   invitation.EmailAddress,
		OrganizationID: invitation.OrganizationID,
		Status:         invitation.Status,
		PublicMetadata: json.RawMessage(invitation.PublicMetadata),
		CreatedAt:      time.UnixMilli(invitation.CreatedAt),
		UpdatedAt:      time.UnixMilli(invitation.UpdatedAt),
	}

	// For non-pending invitations, role might not exist as someone is able to delete it
	if invitation.Role != nil {
		response.Role = invitation.Role.Key
	}

	return response
}

func OrganizationInvitationBAPI(invitation *model.OrganizationInvitationSerializable) *OrganizationInvitationResponse {
	response := OrganizationInvitation(invitation)
	response.PrivateMetadata = json.RawMessage(invitation.PrivateMetadata)
	return response
}

// OrganizationInvitationMe constructs the response of the Organization Invitation resource within a user context.
// Instead of the Organization ID, we will include the whole Organization object
func OrganizationInvitationMe(ctx context.Context, invitation *model.OrganizationInvitationSerializable, org *model.Organization) *OrganizationInvitationResponse {
	response := OrganizationInvitation(invitation)
	response.OrganizationID = ""
	response.PublicOrganizationData = publicOrganizationData(ctx, org)
	return response
}
