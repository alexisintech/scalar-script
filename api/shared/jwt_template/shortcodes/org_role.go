package shortcodes

import (
	"context"

	"clerk/model"
)

type OrgRole struct {
	activeOrgMembership *model.OrganizationMembershipWithDeps
}

func NewOrgRole(m *model.OrganizationMembershipWithDeps) *OrgRole {
	return &OrgRole{
		activeOrgMembership: m,
	}
}

func (s *OrgRole) Identifier() string {
	return "org.role"
}

func (s *OrgRole) Substitute(_ context.Context) (any, error) {
	if s.activeOrgMembership == nil {
		return nil, nil
	}

	return s.activeOrgMembership.Role.Key, nil
}
