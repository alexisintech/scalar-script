package shortcodes

import (
	"context"

	"clerk/model"
)

type OrgHasImage struct {
	activeOrgMembership *model.OrganizationMembershipWithDeps
}

func NewOrgHasImage(m *model.OrganizationMembershipWithDeps) *OrgHasImage {
	return &OrgHasImage{
		activeOrgMembership: m,
	}
}

func (s *OrgHasImage) Identifier() string {
	return "org.has_image"
}

func (s *OrgHasImage) Substitute(_ context.Context) (any, error) {
	if s.activeOrgMembership == nil {
		return nil, nil
	}

	return s.activeOrgMembership.Organization.LogoPublicURL.Valid, nil
}
