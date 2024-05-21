package serialize

import (
	"context"
	"encoding/json"

	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/time"
	"clerk/pkg/versions"

	"github.com/jonboulle/clockwork"
)

type SignUpResponse struct {
	Object           string              `json:"object"`
	ID               string              `json:"id"`
	Status           string              `json:"status"`
	RequiredFields   []string            `json:"required_fields"`
	OptionalFields   []string            `json:"optional_fields"`
	MissingFields    []string            `json:"missing_fields"`
	UnverifiedFields []string            `json:"unverified_fields"`
	Verifications    SignUpVerifications `json:"verifications"`

	Username     *string `json:"username"`
	EmailAddress *string `json:"email_address"`
	PhoneNumber  *string `json:"phone_number"`
	Web3Wallet   *string `json:"web3_wallet"`

	PasswordEnabled  bool            `json:"password_enabled"`
	FirstName        *string         `json:"first_name"`
	LastName         *string         `json:"last_name"`
	UnsafeMetadata   json.RawMessage `json:"unsafe_metadata,omitempty" logger:"omit"`
	PublicMetadata   json.RawMessage `json:"public_metadata,omitempty" logger:"omit"`
	CustomAction     bool            `json:"custom_action"`
	ExternalID       *string         `json:"external_id"`
	CreatedSessionID *string         `json:"created_session_id"`
	CreatedUserID    *string         `json:"created_user_id"`
	AbandonAt        int64           `json:"abandon_at"`

	ExternalAccount interface{} `json:"external_account,omitempty"` // DX: Deprecated >= 3
}

type SignUpVerifications struct {
	EmailAddress *signUpVerificationResponse `json:"email_address"`
	PhoneNumber  *signUpVerificationResponse `json:"phone_number"`
	// TODO: Can we use external account verifications for Oauth and web3 instead of having a fixed Web3Wallet field?
	Web3Wallet      *signUpVerificationResponse `json:"web3_wallet"`
	ExternalAccount *VerificationResponse       `json:"external_account"`
}

func SignUp(ctx context.Context, clock clockwork.Clock, signup *model.SignUpSerializable) (*SignUpResponse, error) {
	signupResponse := SignUpResponse{
		Object:           "sign_up_attempt",
		ID:               signup.ID,
		Status:           signup.Status(clock),
		AbandonAt:        time.UnixMilli(signup.AbandonAt),
		RequiredFields:   signup.RequiredFields,
		OptionalFields:   signup.OptionalFields,
		MissingFields:    signup.MissingFields,
		UnverifiedFields: signup.UnverifiedFields,
		PasswordEnabled:  signup.PasswordDigest.Valid,
		CustomAction:     signup.CustomAction,
		ExternalID:       signup.ExternalID.Ptr(),
	}

	if signup.CreatedSessionID.Valid {
		signupResponse.CreatedSessionID = &signup.CreatedSessionID.String
	}

	if signup.CreatedUserID.Valid {
		signupResponse.CreatedUserID = &signup.CreatedUserID.String
	}

	signupResponse.FirstName = signup.FirstName
	signupResponse.LastName = signup.LastName
	signupResponse.Username = signup.Username
	signupResponse.EmailAddress = signup.EmailAddress
	signupResponse.PhoneNumber = signup.PhoneNumber
	signupResponse.Web3Wallet = signup.Web3Wallet

	if signup.EmailAddressVerification != nil {
		verificationResponse := Verification(signup.EmailAddressVerification)
		signupResponse.Verifications.EmailAddress = newSignUpVerificationResponse(verificationResponse)
	}

	if signup.PhoneNumberVerification != nil {
		verificationResponse := Verification(signup.PhoneNumberVerification)
		signupResponse.Verifications.PhoneNumber = newSignUpVerificationResponse(verificationResponse)
	}

	if signup.Web3WalletVerification != nil {
		verificationResponse := Verification(signup.Web3WalletVerification)
		signupResponse.Verifications.Web3Wallet = newSignUpVerificationResponse(verificationResponse)
	}

	if signup.ExternalAccountVerification != nil {
		signupResponse.Verifications.ExternalAccount = Verification(signup.ExternalAccountVerification)
	}

	// For clerk.js versions < 3, we must include the external account field to ensure backwards-compatibility
	clerkJSVersion := clerkjs_version.FromContext(ctx)
	includeExternalAccount := versions.IsBefore(clerkJSVersion, "3.0.0", true)

	// DX: Deprecated >= 3
	if includeExternalAccount && signup.SuccessfulExternalAccountIdentification != nil && signup.SuccessfulExternalAccountIdentification.IsOAuth() {
		signupResponse.ExternalAccount = externalAccountForIdentification(ctx,
			signup.SuccessfulExternalAccountIdentification)
	}

	if signup.UnsafeMetadata != nil {
		signupResponse.UnsafeMetadata = json.RawMessage(signup.UnsafeMetadata)
	}

	if signup.PublicMetadata != nil {
		signupResponse.PublicMetadata = json.RawMessage(signup.PublicMetadata)
	}

	return &signupResponse, nil
}

// Base class for additional fields in sign up verification
// responses.
type signUpVerificationResponse struct {
	*VerificationResponse

	// NextAction is a signal for the receiver regarding the
	// next steps in order to successfully complete this
	// verification.
	NextAction string `json:"next_action"`
	// SupportedStrategies lists all the available strategies
	// that can be used to complete this verification.
	SupportedStrategies []string `json:"supported_strategies"`
}

func newSignUpVerificationResponse(v *VerificationResponse) *signUpVerificationResponse {
	return &signUpVerificationResponse{
		VerificationResponse: v,
		NextAction:           verificationNextActionFor(v.Status),
		SupportedStrategies:  []string{v.Strategy},
	}
}

// Depending on the status, the verification might need to
// be prepared or attempted.
func verificationNextActionFor(status string) string {
	switch status {
	case constants.VERFailed, constants.VERExpired, "":
		return constants.VERNextActionNeedsPrepare
	case constants.VERUnverified:
		return constants.VERNextActionNeedsAttempt
	default:
		return ""
	}
}
