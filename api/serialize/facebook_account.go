package serialize

import (
	"encoding/json"

	"clerk/model"
	"clerk/pkg/time"
)

// DEPRECATED: use externalAccountResponse instead, for new providers
type facebookAccountResponse struct {
	Object         string          `json:"object"`
	ID             string          `json:"id"`
	FacebookID     string          `json:"facebook_id"`
	ApprovedScopes string          `json:"approved_scopes"`
	EmailAddress   string          `json:"email_address"`
	FirstName      string          `json:"first_name"`
	LastName       string          `json:"last_name"`
	Picture        string          `json:"picture"`
	Username       *string         `json:"username"`
	PublicMetadata json.RawMessage `json:"public_metadata" logger:"omit"`
	Label          *string         `json:"label"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`

	Verification *VerificationResponse `json:"verification"`
}

// DEPRECATED: use externalAccount instead, for new providers
//
// TODO(oauth): drop this when no clients are using these old paylods. And
// simplify the places where this is used.
func facebookAccount(account *model.ExternalAccount, verification *model.VerificationWithStatus) facebookAccountResponse {
	r := facebookAccountResponse{
		Object:         "facebook_account",
		ID:             account.IdentificationID,
		FacebookID:     account.ProviderUserID,
		ApprovedScopes: account.ApprovedScopes,
		EmailAddress:   account.EmailAddress,
		FirstName:      account.FirstName,
		LastName:       account.LastName,
		Picture:        account.AvatarURL,
		PublicMetadata: json.RawMessage(account.PublicMetadata),
		CreatedAt:      time.UnixMilli(account.CreatedAt),
		UpdatedAt:      time.UnixMilli(account.UpdatedAt),
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
