package serialize

import (
	"context"

	"clerk/model"
	"clerk/pkg/time"
)

const OrganizationSuggestionObjectName = "organization_suggestion"

type OrganizationSuggestionResponse struct {
	Object                 string                          `json:"object"`
	ID                     string                          `json:"id"`
	PublicOrganizationData *publicOrganizationDataResponse `json:"public_organization_data,omitempty" logger:"omit"`
	Status                 string                          `json:"status"`
	CreatedAt              int64                           `json:"created_at"`
	UpdatedAt              int64                           `json:"updated_at"`
}

// OrganizationSuggestionMe constructs the response of the Organization Suggestion resource within a user context.
func OrganizationSuggestionMe(ctx context.Context, suggestion *model.OrganizationSuggestion, org *model.Organization) *OrganizationSuggestionResponse {
	return &OrganizationSuggestionResponse{
		Object:                 OrganizationSuggestionObjectName,
		ID:                     suggestion.ID,
		Status:                 suggestion.Status,
		PublicOrganizationData: publicOrganizationData(ctx, org),
		CreatedAt:              time.UnixMilli(suggestion.CreatedAt),
		UpdatedAt:              time.UnixMilli(suggestion.UpdatedAt),
	}
}
