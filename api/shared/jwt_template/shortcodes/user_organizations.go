package shortcodes

import (
	"context"

	"clerk/model"
)

type UserOrganizations struct {
	orgMemberships model.OrganizationMembershipsWithRole
}

func NewUserOrganizations(m model.OrganizationMembershipsWithRole) *UserOrganizations {
	return &UserOrganizations{
		orgMemberships: m,
	}
}

func (s *UserOrganizations) Identifier() string {
	return "user.organizations"
}

func (s *UserOrganizations) Substitute(_ context.Context) (any, error) {
	return s.orgMemberships.ToClaims(), nil
}
