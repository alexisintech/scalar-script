package shortcodes

import (
	"context"

	"clerk/model"
	"clerk/pkg/externalapis/clerkimages"
)

type OrgImageURL struct {
	activeOrgMembership *model.OrganizationMembershipWithDeps
}

func NewOrgImageURL(m *model.OrganizationMembershipWithDeps) *OrgImageURL {
	return &OrgImageURL{
		activeOrgMembership: m,
	}
}

func (s *OrgImageURL) Identifier() string {
	return "org.image_url"
}

func (s *OrgImageURL) Substitute(_ context.Context) (any, error) {
	if s.activeOrgMembership == nil {
		return nil, nil
	}

	org := s.activeOrgMembership.Organization
	opts := clerkimages.NewProxyOrDefaultOptions(org.LogoPublicURL.Ptr(), org.InstanceID, org.GetInitials(), org.ID)

	return clerkimages.GenerateImageURL(opts)
}
