package shortcodes

import (
	"context"

	"clerk/model"
)

type OrgMembershipPermissions struct {
	activeOrgMembership *model.OrganizationMembershipWithDeps
}

func NewOrgMembershipPermissions(m *model.OrganizationMembershipWithDeps) *OrgMembershipPermissions {
	return &OrgMembershipPermissions{
		activeOrgMembership: m,
	}
}

func (s *OrgMembershipPermissions) Identifier() string {
	return "org_membership.permissions"
}

func (s *OrgMembershipPermissions) Substitute(_ context.Context) (any, error) {
	if s.activeOrgMembership == nil {
		return nil, nil
	}

	return s.activeOrgMembership.CustomPermissionKeys(), nil
}
