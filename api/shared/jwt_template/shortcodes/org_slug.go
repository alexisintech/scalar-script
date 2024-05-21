package shortcodes

import (
	"context"

	"clerk/model"
)

type OrgSlug struct {
	activeOrgMembership *model.OrganizationMembershipWithDeps
}

func NewOrgSlug(m *model.OrganizationMembershipWithDeps) *OrgSlug {
	return &OrgSlug{
		activeOrgMembership: m,
	}
}

func (s *OrgSlug) Identifier() string {
	return "org.slug"
}

func (s *OrgSlug) Substitute(_ context.Context) (any, error) {
	if s.activeOrgMembership == nil {
		return nil, nil
	}

	return s.activeOrgMembership.Organization.Slug, nil
}
