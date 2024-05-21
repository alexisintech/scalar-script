package serialize

import (
	"context"
	"encoding/json"

	"clerk/model"
	"clerk/pkg/time"
)

// ObjectOrganizationMembership is the name for organization membership objects.
const ObjectOrganizationMembership = "organization_membership"

type organizationPublicUserData struct {
	publicUserData
	UserID string `json:"user_id"`
}

// OrganizationMembershipResponse is the serialized representation
// for an organization membership.
type OrganizationMembershipResponse struct {
	Object          string          `json:"object"`
	ID              string          `json:"id"`
	PublicMetadata  json.RawMessage `json:"public_metadata" logger:"omit"`
	PrivateMetadata json.RawMessage `json:"private_metadata,omitempty" logger:"omit"`
	Role            string          `json:"role"`
	Permissions     []string        `json:"permissions"`
	CreatedAt       int64           `json:"created_at"`
	UpdatedAt       int64           `json:"updated_at"`

	Organization   *OrganizationResponse       `json:"organization"`
	PublicUserData *organizationPublicUserData `json:"public_user_data,omitempty"`
}

// OrganizationMembership converts a model.OrganizationMembership to
// an OrganizationMembershipResponse.
func OrganizationMembership(ctx context.Context, membership *model.OrganizationMembershipSerializable) *OrganizationMembershipResponse {
	response := organizationMembership(membership)
	response.Organization = Organization(
		ctx,
		&membership.Organization,
		WithMembersCount(membership.MembersCount),
		WithPendingInvitationsCount(membership.PendingInvitationsCount),
		WithBillingPlan(membership.BillingPlan))
	return response
}

func OrganizationMembershipBAPI(ctx context.Context, membership *model.OrganizationMembershipSerializable) *OrganizationMembershipResponse {
	response := organizationMembership(membership)
	response.PrivateMetadata = json.RawMessage(membership.OrganizationMembership.PrivateMetadata)
	response.Organization = OrganizationBAPI(ctx, &membership.Organization)
	return response
}

func organizationMembership(organizationMembership *model.OrganizationMembershipSerializable) *OrganizationMembershipResponse {
	resp := OrganizationMembershipResponse{
		Object:         ObjectOrganizationMembership,
		ID:             organizationMembership.OrganizationMembership.ID,
		PublicMetadata: json.RawMessage(organizationMembership.OrganizationMembership.PublicMetadata),
		Role:           organizationMembership.Role.Key,
		Permissions:    organizationMembership.PermissionKeys,
		CreatedAt:      time.UnixMilli(organizationMembership.OrganizationMembership.CreatedAt),
		UpdatedAt:      time.UnixMilli(organizationMembership.OrganizationMembership.UpdatedAt),
	}

	if organizationMembership.User.User != nil {
		resp.PublicUserData = &organizationPublicUserData{
			publicUserData: publicUserData{
				FirstName:       organizationMembership.User.FirstName.Ptr(),
				LastName:        organizationMembership.User.LastName.Ptr(),
				ProfileImageURL: organizationMembership.ProfileImageURL,
				Identifier:      organizationMembership.Identifier,
				ImageURL:        organizationMembership.ImageURL,
				HasImage:        organizationMembership.User.ProfileImagePublicURL.Valid,
			},
			UserID: organizationMembership.User.ID,
		}
	}

	return &resp
}
