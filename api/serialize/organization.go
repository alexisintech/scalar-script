package serialize

import (
	"context"
	"encoding/json"

	"clerk/model"
	"clerk/pkg/externalapis/clerkimages"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/time"
)

// ObjectOrganization is the name for organization objects.
const ObjectOrganization = "organization"

// OrganizationResponse is the default serialization representation
// for an organization object.
type OrganizationResponse struct {
	Object                  string          `json:"object"`
	ID                      string          `json:"id"`
	Name                    string          `json:"name"`
	Slug                    string          `json:"slug"`
	ImageURL                string          `json:"image_url,omitempty"`
	HasImage                bool            `json:"has_image"`
	MembersCount            *int            `json:"members_count,omitempty"`
	PendingInvitationsCount *int            `json:"pending_invitations_count,omitempty"`
	MaxAllowedMemberships   int             `json:"max_allowed_memberships"`
	AdminDeleteEnabled      bool            `json:"admin_delete_enabled"`
	PublicMetadata          json.RawMessage `json:"public_metadata" logger:"omit"`
	PrivateMetadata         json.RawMessage `json:"private_metadata,omitempty" logger:"omit"`
	BillingPlan             *string         `json:"plan,omitempty"`
	CreatedBy               string          `json:"created_by,omitempty"`
	CreatedAt               int64           `json:"created_at"`
	UpdatedAt               int64           `json:"updated_at"`

	// DEPRECATED: After 4.36.0
	LogoURL *string `json:"logo_url"`
}

type publicOrganizationDataResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	ImageURL string `json:"image_url,omitempty"`
	HasImage bool   `json:"has_image"`
}

// Organization will return a default serialization object
// for the provided model.Organization.
func Organization(ctx context.Context, org *model.Organization, options ...func(*OrganizationResponse)) *OrganizationResponse {
	response := &OrganizationResponse{
		Object:                ObjectOrganization,
		ID:                    org.ID,
		Name:                  org.Name,
		Slug:                  org.Slug,
		LogoURL:               org.GetLogoURL(),
		ImageURL:              organizationImageURL(ctx, org),
		HasImage:              org.LogoPublicURL.Valid,
		PublicMetadata:        json.RawMessage(org.PublicMetadata),
		MaxAllowedMemberships: org.MaxAllowedMemberships,
		AdminDeleteEnabled:    org.AdminDeleteEnabled,
		CreatedAt:             time.UnixMilli(org.CreatedAt),
		UpdatedAt:             time.UnixMilli(org.UpdatedAt),
	}

	for _, option := range options {
		option(response)
	}
	return response
}

// OrganizationBAPI returns the default serialization object for
// the provided model.Organization, adding attributes that make
// sense only to the backend API.
func OrganizationBAPI(ctx context.Context, org *model.Organization, options ...func(*OrganizationResponse)) *OrganizationResponse {
	res := Organization(ctx, org, options...)
	res.PrivateMetadata = json.RawMessage(org.PrivateMetadata)
	res.CreatedBy = org.CreatedBy
	return res
}

func WithMembersCount(count int) func(*OrganizationResponse) {
	return func(response *OrganizationResponse) {
		response.MembersCount = &count
	}
}

func WithPendingInvitationsCount(count int) func(*OrganizationResponse) {
	return func(response *OrganizationResponse) {
		response.PendingInvitationsCount = &count
	}
}

func WithBillingPlan(planKey *string) func(*OrganizationResponse) {
	return func(response *OrganizationResponse) {
		response.BillingPlan = planKey
	}
}

func organizationImageURL(ctx context.Context, org *model.Organization) string {
	imageURLOptions := clerkimages.NewProxyOrDefaultOptions(org.LogoPublicURL.Ptr(), org.InstanceID, org.GetInitials(), org.ID)
	imageURL, err := clerkimages.GenerateImageURL(imageURLOptions)
	// This error should never happen, but if it happens
	// we add this notification and return empty string as ImageURL
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}
	return imageURL
}

func publicOrganizationData(ctx context.Context, org *model.Organization) *publicOrganizationDataResponse {
	return &publicOrganizationDataResponse{
		ID:       org.ID,
		Name:     org.Name,
		Slug:     org.Slug,
		ImageURL: organizationImageURL(ctx, org),
		HasImage: org.LogoPublicURL.Valid,
	}
}
