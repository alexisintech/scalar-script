package serialize

import (
	"encoding/json"

	"clerk/model"
)

type SAMLAccountResponse struct {
	Object         string          `json:"object"`
	ID             string          `json:"id"`
	Provider       string          `json:"provider"`
	Active         bool            `json:"active"`
	EmailAddress   string          `json:"email_address"`
	FirstName      *string         `json:"first_name"`
	LastName       *string         `json:"last_name"`
	ProviderUserID *string         `json:"provider_user_id"`
	PublicMetadata json.RawMessage `json:"public_metadata" logger:"omit"`

	Verification *VerificationResponse `json:"verification"`
}

func SAMLAccount(account *model.SAMLAccountWithDeps, verification *model.VerificationWithStatus) *SAMLAccountResponse {
	r := &SAMLAccountResponse{
		Object:         "saml_account",
		ID:             account.ID,
		Provider:       account.SAMLConnection.Provider,
		Active:         account.SAMLConnection.Active,
		EmailAddress:   account.EmailAddress,
		FirstName:      account.FirstName.Ptr(),
		LastName:       account.LastName.Ptr(),
		ProviderUserID: account.ProviderUserID.Ptr(),
		PublicMetadata: json.RawMessage(account.PublicMetadata),
	}

	if verification != nil {
		r.Verification = Verification(verification)
	}

	return r
}
