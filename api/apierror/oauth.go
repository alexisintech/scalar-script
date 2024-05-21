package apierror

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// InvalidOAuthCallback signifies an error when the form of OAuth callback is invalid
func InvalidOAuthCallback() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid OAuth callback",
		longMessage:  "invalid form for oauth_callback",
		code:         OAuthCallbackInvalidCode,
	})
}

// ExternalAccountNotFound signifies an error when the external account of the oauth callback is not found
func ExternalAccountNotFound() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Invalid external account",
		longMessage:  "The External Account was not found.",
		code:         ExternalAccountNotFoundCode,
	})
}

// ExternalAccountEmailAddressVerificationRequired signifies an error when the external account requires email address verification
func ExternalAccountEmailAddressVerificationRequired() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Email address verification required",
		longMessage:  "Your associated email address is required to be verified, because it was initially created as unverified.",
		code:         ExternalAccountEmailAddressVerificationRequiredCode,
	})
}

func ExternalAccountMissingRefreshToken() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Missing refresh token",
		longMessage:  "We cannot refresh your OAuth access token because the server didn't provide a refresh token. Please re-connect your account.",
		code:         ExternalAccountMissingRefreshTokenCode,
	})
}

// OAuthAccountAlreadyConnected signifies an error when an OAuth account if already connected for a specific provider
func OAuthAccountAlreadyConnected(providerID string) Error {
	// TODO(oauth): Remove this when we unify sso provider IDs (facebook vs oauth_facebook)
	providerID = strings.TrimPrefix(providerID, "oauth_")
	providerTitle := cases.Title(language.Und, cases.NoLower).String(providerID)

	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Already connected",
		longMessage:  fmt.Sprintf("Another account is already connected for this particular provider (%s)", providerTitle),
		code:         OAuthAccountAlreadyConnectedCode,
	})
}

func OAuthAccessDenied(providerName string) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: fmt.Sprintf("Access denied to %s account", providerName),
		longMessage:  fmt.Sprintf("You did not grant access to your %s account", providerName),
		code:         OAuthAccessDeniedCode,
	})
}

func OAuthInvalidRedirectURI(providerName string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: fmt.Sprintf("invalid redirect uri configuration in %s", providerName),
		longMessage: fmt.Sprintf("Your %s account configuration is invalid. Make sure you register this endpoint in the list of allowed callback URLs.",
			providerName),
		code: OAuthRedirectURIMismatch,
	})
}

// MisconfiguredOAuthProvider signifies an error when there is a misconfiguration for an OAuth provider
func MisconfiguredOAuthProvider() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Misconfigured OAuth provider",
		longMessage:  "Misconfigured OAuth provider. Please make sure you have set it correctly",
		code:         OAuthMisconfiguredProviderCode,
	})
}

// OAuthSharedCredentialsNotSupported signifies an error when an OAuth provider uses our shared credentials, but those are not supported anymore.
func OAuthSharedCredentialsNotSupported() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Shared credentials not supported",
		longMessage:  "Shared credentials are no longer supported for this provider. Please update via the Clerk Dashboard.",
		code:         OAuthSharedCredentialsNotSupportedCode,
	})
}

// OAuthIdentificationClaimed signifies an error when the requested oauth identification is already claimed by another user
func OAuthIdentificationClaimed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Identification claimed by another user",
		longMessage:  "The email address associated with this OAuth account is already claimed by another user.",
		code:         OAuthIdentificationClaimedCode,
	})
}

func OAuthTokenExchangeError(err error) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Token exchange error",
		longMessage:  err.Error(),
		code:         OAuthTokenExchangeErrorCode,
	})
}

func OAuthFetchUserError(err error) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Fetch user error",
		longMessage:  err.Error(),
		code:         OAuthFetchUserErrorCode,
	})
}

// OAuthConfigMissing signifies an error when an application does not have
// SSO credentials set, for a particular SSO provider.
func OAuthConfigMissing(provider string) Error {
	// TODO(oauth): we should introduce a model.SSOProvider interface which
	// will have a Title() or Label() method and use that here, instead of
	// accepting a string and doing fancy stuff with it.
	provider = cases.Title(language.Und, cases.NoLower).String(strings.TrimPrefix(provider, "oauth_"))

	return New(http.StatusBadRequest, &mainError{
		shortMessage: provider + " OAuth keys are missing",
		longMessage:  "The application does not have " + provider + " OAuth keys set in its settings.",
		code:         OAuthConfigMissingCode,
	})
}

func OAuthMissingRefreshToken() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Cannot refresh OAuth access token",
		longMessage: "The current access token has expired and we cannot " +
			"refresh it, because the authorization server hasn't provided us " +
			"with a refresh token",
		code: OAuthMissingRefreshTokenCode,
	})
}

func OAuthMissingAccessToken() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Missing OAuth access token",
		longMessage:  "OAuth access token is missing",
		code:         OAuthMissingAccessTokenCode,
	})
}

func OAuthTokenRetrievalError(cause error) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Token retrieval failed",
		longMessage:  "Failed to retrieve a new access token from the OAuth provider",
		meta:         &oauthTokenWalletMeta{ProviderError: cause.Error()},
		code:         OauthTokenRetrievalErrorCode,
	})
}

func OAuthProviderNotEnabled(providerID string) Error {
	// TODO(oauth): Remove this when we unify sso provider IDs (facebook vs oauth_facebook)
	providerID = strings.TrimPrefix(providerID, "oauth_")
	providerTitle := cases.Title(language.Und, cases.NoLower).String(providerID)

	return New(http.StatusBadRequest, &mainError{
		shortMessage: fmt.Sprintf("%v OAuth provider not enabled", providerTitle),
		longMessage:  fmt.Sprintf("Single-sign on with %s OAuth provider is not enabled in the instance settings.", providerTitle),
		code:         OAuthProviderNotEnabledCode,
	})
}

func OAuthTokenProviderNotEnabled() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "OAuth provider not enabled",
		longMessage:  "Single-sign on for this OAuth provider is not enabled in the instance settings.",
		code:         OAuthTokenProviderNotEnabledCode,
	})
}

// UnsupportedOauthProvider signifies an error when an instance tries to enable
// an OAuth external provider which is not supported.
func UnsupportedOauthProvider(oauthProviderID string) Error {
	// TODO(oauth): Remove this when we unify sso provider IDs (facebook vs oauth_facebook)
	oauthProviderID = strings.TrimPrefix(oauthProviderID, "oauth_")
	providerTitle := cases.Title(language.Und, cases.NoLower).String(oauthProviderID)

	return New(http.StatusBadRequest, &mainError{
		shortMessage: fmt.Sprintf("%v OAuth is not supported.", providerTitle),
		longMessage:  fmt.Sprintf("%v OAuth is not supported. Please contact us if you think this error should not appear.", providerTitle),
		code:         OAuthUnsupportedProviderCode,
	})
}

// NonAuthenticatableOauthProvider signifies an error when an oauth flow step is attempted for a provider that is not
// enabled for authentication.
func NonAuthenticatableOauthProvider(oauthProviderID string) Error {
	oauthProviderID = strings.TrimPrefix(oauthProviderID, "oauth_")
	providerTitle := cases.Title(language.Und, cases.NoLower).String(oauthProviderID)

	return New(http.StatusBadRequest, &mainError{
		shortMessage: fmt.Sprintf("%v OAuth is not supported for authentication.", providerTitle),
		longMessage:  fmt.Sprintf("%v OAuth is not supported for authentication. Please contact us if you think this error should not appear.", providerTitle),
		code:         OauthNonAuthenticatableProviderCode,
	})
}
