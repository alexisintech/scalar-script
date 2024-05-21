package serialize

import (
	"context"
	"encoding/json"

	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/ctx/sdkversion"
	"clerk/pkg/oauth"
	"clerk/pkg/time"
	"clerk/pkg/versions"
)

const UserObjectName = "user"

type UserResponse struct {
	ID                            string                            `json:"id"`
	Object                        string                            `json:"object"`
	Username                      *string                           `json:"username"`
	FirstName                     *string                           `json:"first_name"`
	LastName                      *string                           `json:"last_name"`
	ImageURL                      string                            `json:"image_url,omitempty"`
	HasImage                      bool                              `json:"has_image"`
	PrimaryEmailAddressID         *string                           `json:"primary_email_address_id"`
	PrimaryPhoneNumberID          *string                           `json:"primary_phone_number_id"`
	PrimaryWeb3WalletID           *string                           `json:"primary_web3_wallet_id"`
	PasswordEnabled               bool                              `json:"password_enabled"`
	TwoFactorEnabled              bool                              `json:"two_factor_enabled"`
	TOTPEnabled                   bool                              `json:"totp_enabled"`
	BackupCodeEnabled             bool                              `json:"backup_code_enabled"`
	EmailAddresses                []*EmailAddressResponse           `json:"email_addresses"`
	PhoneNumbers                  []*PhoneNumberResponse            `json:"phone_numbers"`
	Web3Wallets                   []*Web3WalletResponse             `json:"web3_wallets"`
	Passkeys                      []*PasskeyResponse                `json:"passkeys"`
	OrganizationMemberships       []*OrganizationMembershipResponse `json:"organization_memberships,omitempty"`
	ExternalAccounts              []interface{}                     `json:"external_accounts"`
	SAMLAccounts                  []*SAMLAccountResponse            `json:"saml_accounts"`
	PasswordLastUpdatedAt         *int64                            `json:"password_last_updated_at,omitempty"`
	PublicMetadata                json.RawMessage                   `json:"public_metadata" logger:"omit"`
	PrivateMetadata               json.RawMessage                   `json:"private_metadata,omitempty" logger:"omit"`
	UnsafeMetadata                json.RawMessage                   `json:"unsafe_metadata,omitempty" logger:"omit"`
	ExternalID                    *string                           `json:"external_id"`
	LastSignInAt                  *int64                            `json:"last_sign_in_at"`
	Banned                        bool                              `json:"banned"`
	Locked                        bool                              `json:"locked"`
	LockoutExpiresInSeconds       *int64                            `json:"lockout_expires_in_seconds"`
	VerificationAttemptsRemaining *int64                            `json:"verification_attempts_remaining"`
	CreatedAt                     int64                             `json:"created_at"`
	UpdatedAt                     int64                             `json:"updated_at"`
	DeleteSelfEnabled             bool                              `json:"delete_self_enabled"`
	CreateOrganizationEnabled     bool                              `json:"create_organization_enabled"`
	LastActiveAt                  *int64                            `json:"last_active_at"`
	BillingPlan                   *string                           `json:"plan,omitempty"`

	// DEPRECATED: After 4.36.0
	ProfileImageURL string `json:"profile_image_url"`
}

type sessionUserResponse struct {
	*UserResponse
	OrganizationMemberships []*OrganizationMembershipResponse `json:"organization_memberships"`
}

func UserToServerAPI(ctx context.Context, user *model.UserSerializable) *UserResponse {
	// For BAPI and Go-SDK version < 2, we must respond with the legacy payload to ensure backwards-compatibility
	useLegacyExtAccount := useLegacyExtAccountForSDK(ctx)

	response := userResponse(ctx, user, useLegacyExtAccount)
	response.ID = user.ID
	response.PrivateMetadata = json.RawMessage(user.PrivateMetadata)
	return response
}

func UserToClientAPI(ctx context.Context, user *model.UserSerializable) *UserResponse {
	// For FAPI and clerk.js versions < 3, we must respond with the legacy payload
	// to ensure backwards-compatibility
	clerkJSVersion := clerkjs_version.FromContext(ctx)
	useLegacyExtAccount := versions.IsBefore(clerkJSVersion, "3.0.0", true)

	return userResponse(ctx, user, useLegacyExtAccount)
}

func UserToDashboardAPI(ctx context.Context, user *model.UserSerializable) *UserResponse {
	response := userResponse(ctx, user, false)
	response.ID = user.ID
	response.PrivateMetadata = json.RawMessage(user.PrivateMetadata)

	if user.PasswordLastUpdatedAt.Valid {
		lastUpdated := time.UnixMilli(user.PasswordLastUpdatedAt.Time)
		response.PasswordLastUpdatedAt = &lastUpdated
	}

	return response
}

func sessionUser(ctx context.Context, session *model.SessionWithUser) *sessionUserResponse {
	memberships := make([]*OrganizationMembershipResponse, len(session.OrganizationMemberships))
	for i, membership := range session.OrganizationMemberships {
		memberships[i] = OrganizationMembership(ctx, membership)
		// SessionUserResponse is part of a User response, which already has all user data.
		// Hence, we don't want to add them yet another time
		memberships[i].PublicUserData = nil
	}

	return &sessionUserResponse{
		UserResponse:            UserToClientAPI(ctx, session.User),
		OrganizationMemberships: memberships,
	}
}

func userResponse(ctx context.Context, user *model.UserSerializable, useLegacyExtAccount bool) *UserResponse {
	userResStruct := UserResponse{
		ID:                            user.ID,
		Object:                        UserObjectName,
		PasswordEnabled:               user.PasswordDigest.Valid,
		PublicMetadata:                json.RawMessage(user.PublicMetadata),
		PrivateMetadata:               json.RawMessage(nil),
		UnsafeMetadata:                json.RawMessage(user.UnsafeMetadata),
		ProfileImageURL:               user.ProfileImageURL,
		ImageURL:                      user.ImageURL,
		HasImage:                      user.User.ProfileImagePublicURL.Valid,
		TwoFactorEnabled:              user.TwoFactorEnabled,
		TOTPEnabled:                   user.TOTPEnabled,
		BackupCodeEnabled:             user.BackupCodeEnabled,
		ExternalAccounts:              make([]interface{}, 0),
		SAMLAccounts:                  make([]*SAMLAccountResponse, 0),
		Banned:                        user.Banned,
		Locked:                        user.Locked,
		LockoutExpiresInSeconds:       user.LockoutExpiresInSeconds,
		VerificationAttemptsRemaining: user.VerificationAttemptsRemaining,
		CreatedAt:                     time.UnixMilli(user.CreatedAt),
		UpdatedAt:                     time.UnixMilli(user.UpdatedAt),
		DeleteSelfEnabled:             user.DeleteSelfEnabled,
		CreateOrganizationEnabled:     user.CreateOrganizationEnabled,
		BillingPlan:                   user.BillingPlan,
	}

	if user.FirstName.Valid {
		userResStruct.FirstName = &user.FirstName.String
	}

	if user.LastName.Valid {
		userResStruct.LastName = &user.LastName.String
	}

	if user.PrimaryEmailAddressID.Valid {
		userResStruct.PrimaryEmailAddressID = &user.PrimaryEmailAddressID.String
	}

	if user.PrimaryPhoneNumberID.Valid {
		userResStruct.PrimaryPhoneNumberID = &user.PrimaryPhoneNumberID.String
	}

	if user.PrimaryWeb3WalletID.Valid {
		userResStruct.PrimaryWeb3WalletID = &user.PrimaryWeb3WalletID.String
	}

	if user.ExternalID.Valid {
		userResStruct.ExternalID = &user.ExternalID.String
	}

	if user.Username != nil {
		userResStruct.Username = user.Username
	}

	if user.LastSignInAt.Valid {
		lastSignIn := time.UnixMilli(user.LastSignInAt.Time)
		userResStruct.LastSignInAt = &lastSignIn
	}

	if user.LastActiveAt.Valid {
		v := time.UnixMilli(user.LastActiveAt.Time)
		userResStruct.LastActiveAt = &v
	}

	// Email Addresses
	userResStruct.EmailAddresses = emailAddressesForIdentifications(user.Identifications[constants.ITEmailAddress])

	// Phone Numbers
	userResStruct.PhoneNumbers = phoneNumbersForIdentifications(user.Identifications[constants.ITPhoneNumber])

	// Web3 Wallets
	userResStruct.Web3Wallets = web3WalletsForIdentifications(user.Identifications[constants.ITWeb3Wallet])

	// Passkeys
	userResStruct.Passkeys = make([]*PasskeyResponse, 0)
	if cenv.ResourceHasAccess(cenv.FlagAllowPasskeysInstanceIDs, user.InstanceID) {
		userResStruct.Passkeys = passkeysForIdentifications(user.Identifications[constants.ITPasskey])
	}

	// External Accounts
	externalAccountIdentifications := make([]*model.IdentificationSerializable, 0)
	for _, provider := range oauth.Providers() {
		externalAccountIdentifications = append(externalAccountIdentifications, user.Identifications[provider]...)
	}

	for _, identification := range externalAccountIdentifications {
		if identification.ExternalAccount == nil {
			continue
		}
		if useLegacyExtAccount {
			userResStruct.ExternalAccounts = append(userResStruct.ExternalAccounts, externalAccountForIdentification(ctx, identification))
		} else {
			userResStruct.ExternalAccounts = append(userResStruct.ExternalAccounts, ExternalAccount(ctx, identification.ExternalAccount, identification.Verification))
		}
	}

	// SAML Accounts
	for _, identification := range user.Identifications[constants.ITSAML] {
		userResStruct.SAMLAccounts = append(userResStruct.SAMLAccounts, SAMLAccount(identification.SAMLAccountWithDeps, identification.Verification))
	}

	return &userResStruct
}

type ProxyImageURLResponse struct {
	URL string `json:"url"`
}

func ProxyImageURL(URL string) *ProxyImageURLResponse {
	return &ProxyImageURLResponse{
		URL: URL,
	}
}

func useLegacyExtAccountForSDK(ctx context.Context) bool {
	sdkVersion := sdkversion.FromContext(ctx)
	if !sdkVersion.IsGo() {
		return true
	}
	return versions.IsBefore(sdkVersion.Version(), "2.0.0", true)
}
