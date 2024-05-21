package serialize

import (
	"context"
	"encoding/json"

	"clerk/model"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/oauth/provider"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/time"
)

type ExternalAccountResponse struct {
	Object           string          `json:"object"`
	ID               string          `json:"id"`
	Provider         string          `json:"provider"`
	IdentificationID string          `json:"identification_id"`
	ProviderUserID   string          `json:"provider_user_id"`
	ApprovedScopes   string          `json:"approved_scopes"`
	EmailAddress     string          `json:"email_address"`
	FirstName        string          `json:"first_name"`
	LastName         string          `json:"last_name"`
	AvatarURL        string          `json:"avatar_url"`
	ImageURL         string          `json:"image_url,omitempty"`
	Username         *string         `json:"username"`
	PublicMetadata   json.RawMessage `json:"public_metadata" logger:"omit"`
	Label            *string         `json:"label"`
	CreatedAt        int64           `json:"created_at"`
	UpdatedAt        int64           `json:"updated_at"`

	Verification *VerificationResponse `json:"verification"`
}

func ExternalAccount(ctx context.Context, account *model.ExternalAccount, verification *model.VerificationWithStatus) *ExternalAccountResponse {
	imageURL, err := clerkimages.GenerateImageURL(clerkimages.NewProxyOptions(&account.AvatarURL))
	// This error should never happen, but if it happens
	// we add this notification and return empty string as ImageURL
	if err != nil {
		sentryclerk.CaptureException(ctx, err)
	}

	r := &ExternalAccountResponse{
		Object:           "external_account",
		ID:               account.ID,
		Provider:         account.Provider,
		IdentificationID: account.IdentificationID,
		ProviderUserID:   account.ProviderUserID,
		ApprovedScopes:   account.ApprovedScopes,
		EmailAddress:     account.EmailAddress,
		FirstName:        account.FirstName,
		LastName:         account.LastName,
		AvatarURL:        account.AvatarURL,
		ImageURL:         imageURL,
		PublicMetadata:   json.RawMessage(account.PublicMetadata),
		CreatedAt:        time.UnixMilli(account.CreatedAt),
		UpdatedAt:        time.UnixMilli(account.UpdatedAt),
	}

	if account.Username.Valid {
		r.Username = &account.Username.String
	}

	if account.Label.Valid {
		r.Label = &account.Label.String
	}

	if verification != nil {
		r.Verification = Verification(verification)
	}

	return r
}

func externalAccountForIdentification(ctx context.Context, ident *model.IdentificationSerializable) interface{} {
	switch ident.Type {
	// Ensure backwards compatibility for clerk.js versions <= 2 and SDKs
	case provider.GoogleID():
		return googleAccount(ident.ExternalAccount, ident.Verification)
	case provider.FacebookID():
		return facebookAccount(ident.ExternalAccount, ident.Verification)
	default:
		return ExternalAccount(ctx, ident.ExternalAccount, ident.Verification)
	}
}
