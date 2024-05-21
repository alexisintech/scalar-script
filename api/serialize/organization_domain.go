package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

// ObjectOrganizationDomain is the name for organization domain objects.
const ObjectOrganizationDomain = "organization_domain"

type OrganizationDomainResponse struct {
	Object                  string                         `json:"object"`
	ID                      string                         `json:"id"`
	OrganizationID          string                         `json:"organization_id"`
	Name                    string                         `json:"name"`
	EnrollmentMode          string                         `json:"enrollment_mode"`
	AffiliationEmailAddress *string                        `json:"affiliation_email_address"`
	Verification            *orgDomainVerificationResponse `json:"verification"`
	TotalPendingInvitations int                            `json:"total_pending_invitations"`
	TotalPendingSuggestions int                            `json:"total_pending_suggestions"`
	CreatedAt               int64                          `json:"created_at"`
	UpdatedAt               int64                          `json:"updated_at"`
}

type orgDomainVerificationResponse struct {
	Status   string `json:"status"`
	Strategy string `json:"strategy"`
	Attempts *int   `json:"attempts"`
	ExpireAt *int64 `json:"expire_at"`
}

func OrganizationDomain(orgDomain *model.OrganizationDomainSerializable) *OrganizationDomainResponse {
	return &OrganizationDomainResponse{
		Object:                  ObjectOrganizationDomain,
		ID:                      orgDomain.ID,
		OrganizationID:          orgDomain.OrganizationID,
		Name:                    orgDomain.Name,
		EnrollmentMode:          orgDomain.EnrollmentMode,
		AffiliationEmailAddress: orgDomain.AffiliationEmailAddress.Ptr(),
		Verification:            orgDomainVerification(orgDomain.Verification),
		TotalPendingInvitations: orgDomain.TotalPendingInvitations,
		TotalPendingSuggestions: orgDomain.TotalPendingSuggestions,
		CreatedAt:               time.UnixMilli(orgDomain.CreatedAt),
		UpdatedAt:               time.UnixMilli(orgDomain.UpdatedAt),
	}
}

func orgDomainVerification(verification *model.OrganizationDomainVerificationWithStatus) *orgDomainVerificationResponse {
	if verification == nil {
		return nil
	}

	response := &orgDomainVerificationResponse{
		Status:   verification.Status,
		Strategy: verification.Strategy,
		Attempts: &verification.Attempts,
	}

	if verification.ExpireAt.Valid && !verification.ExpireAt.IsZero() {
		expireAt := time.UnixMilli(verification.ExpireAt.Time)
		response.ExpireAt = &expireAt
	}

	return response
}
