package serialize

import (
	"clerk/api/sapi/v1/serializable"
	"clerk/model"
)

type ApplicationResponse struct {
	ID    string                   `json:"id"`
	Name  string                   `json:"name"`
	Owner ApplicationOwnerResponse `json:"owner"`
}

type ApplicationOwnerResponse struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	HasUnlimitedSeats *bool  `json:"has_unlimited_seats"`
}

func Application(applicationSerializable *serializable.Application) *ApplicationResponse {
	res := &ApplicationResponse{
		ID:   applicationSerializable.Application.ID,
		Name: applicationSerializable.Application.Name,
		Owner: ApplicationOwnerResponse{
			ID:   applicationSerializable.ApplicationOwnership.OwnerID(),
			Type: applicationSerializable.ApplicationOwnership.OwnerType(),
		},
	}

	if applicationSerializable.ApplicationOwnership.IsOrganization() {
		var hasUnlimitedSeats bool
		if applicationSerializable.OwnerOrganizationPlan != nil {
			hasUnlimitedSeats = applicationSerializable.OwnerOrganizationPlan.OrganizationMembershipLimit == model.UnlimitedMemberships
		}
		res.Owner.HasUnlimitedSeats = &hasUnlimitedSeats
	}

	return res
}
