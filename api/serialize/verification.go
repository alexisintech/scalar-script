package serialize

import (
	"encoding/json"

	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/time"
)

type VerificationResponse struct {
	Status           string `json:"status"`
	Strategy         string `json:"strategy"`
	Attempts         *int   `json:"attempts"`
	ExpireAt         *int64 `json:"expire_at"`
	VerifiedAtClient string `json:"verified_at_client,omitempty"`

	// Web3, OAuth, SAML
	Nonce *string `json:"nonce,omitempty"`

	// OAuth, SAML
	ExternalVerificationRedirectURL *string `json:"external_verification_redirect_url,omitempty"`

	Error json.RawMessage `json:"error,omitempty"`
}

func Verification(verification *model.VerificationWithStatus) *VerificationResponse {
	response := &VerificationResponse{
		Status:   verification.Status,
		Strategy: verification.Strategy,
	}

	if !verification.ExpireAt.IsZero() {
		expireAt := time.UnixMilli(verification.ExpireAt)
		response.ExpireAt = &expireAt
	}

	if verification.External() {
		if verification.Status == constants.VERUnverified {
			response.ExternalVerificationRedirectURL = verification.ExternalAuthorizationURL.Ptr()
		}

		if verification.Error.Valid {
			response.Error = verification.Error.JSON
		}

		return response
	}

	// rest of the strategies
	switch verification.Strategy {
	case constants.VSPassword:
		response.Attempts = &verification.Attempts

	case constants.VSEmailCode, constants.VSPhoneCode, constants.VSResetPasswordEmailCode, constants.VSResetPasswordPhoneCode:
		response.Attempts = &verification.Attempts

	case constants.VSEmailLink:
		response.VerifiedAtClient = verification.VerifiedAtClientID.String

	case constants.VSWeb3MetamaskSignature:
		response.Attempts = &verification.Attempts
		response.Nonce = &verification.Nonce.String

	case constants.VSPasskey:
		response.Attempts = &verification.Attempts
		response.Nonce = verification.Nonce.Ptr()
	}

	return response
}
