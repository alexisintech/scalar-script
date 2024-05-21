package shortcodes

import (
	"context"

	"clerk/model"
)

type OrgID struct {
	activeOrgMembership *model.OrganizationMembershipWithDeps
}

func NewOrgID(m *model.OrganizationMembershipWithDeps) *OrgID {
	return &OrgID{
		activeOrgMembership: m,
	}
}

func (s *OrgID) Identifier() string {
	return "org.id"
}

func (s *OrgID) Substitute(_ context.Context) (any, error) {
	if s.activeOrgMembership == nil {
		return nil, nil
	}

	return s.activeOrgMembership.OrganizationID, nil
}
