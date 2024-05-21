package shortcodes

import (
	"context"

	"clerk/model"
)

type OrgName struct {
	activeOrgMembership *model.OrganizationMembershipWithDeps
}

func NewOrgName(m *model.OrganizationMembershipWithDeps) *OrgName {
	return &OrgName{
		activeOrgMembership: m,
	}
}

func (s *OrgName) Identifier() string {
	return "org.name"
}

func (s *OrgName) Substitute(_ context.Context) (any, error) {
	if s.activeOrgMembership == nil {
		return nil, nil
	}

	return s.activeOrgMembership.Organization.Name, nil
}
