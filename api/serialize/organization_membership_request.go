package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const ObjectOrganizationMembershipRequest = "organization_membership_request"

type OrganizationMembershipRequestResponse struct {
	Object         string          `json:"object"`
	ID             string          `json:"id"`
	OrganizationID string          `json:"organization_id"`
	Status         string          `json:"status"`
	PublicUserData *publicUserData `json:"public_user_data" logger:"omit"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`
}

func OrganizationMembershipRequest(membershipReq *model.OrganizationMembershipRequestSerializable) *OrganizationMembershipRequestResponse {
	return &OrganizationMembershipRequestResponse{
		Object:         ObjectOrganizationMembershipRequest,
		ID:             membershipReq.ID,
		OrganizationID: membershipReq.OrganizationID,
		Status:         membershipReq.Status,
		PublicUserData: &publicUserData{
			FirstName:  membershipReq.User.FirstName.Ptr(),
			LastName:   membershipReq.User.LastName.Ptr(),
			ImageURL:   membershipReq.ImageURL,
			HasImage:   membershipReq.User.ProfileImagePublicURL.Valid,
			Identifier: membershipReq.Identifier,
		},
		CreatedAt: time.UnixMilli(membershipReq.CreatedAt),
		UpdatedAt: time.UnixMilli(membershipReq.UpdatedAt),
	}
}
