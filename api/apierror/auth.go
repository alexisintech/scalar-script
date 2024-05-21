package apierror

import (
	"fmt"
	"net/http"
	"strings"

	"clerk/pkg/constants"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/oauth"

	"github.com/dongri/phonenumber"
)

// InvalidClerkSecretKey signifies an error when the supplied client key is invalid
func InvalidClerkSecretKey() Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "The provided Clerk Secret Key is invalid. Make sure that your Clerk Secret Key is correct.",
		code:         ClerkKeyInvalidCode,
	})
}

// InvalidAuthentication signifies an error when the request is not authenticated
func InvalidAuthentication() Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "Invalid authentication",
		longMessage:  "Unable to authenticate the request, you need to supply an active session",
		code:         AuthenticationInvalidCode,
	})
}

// InvalidAuthorizationHeaderFormat signifies an error when the Authorization header has no proper format.
func InvalidAuthorizationHeaderFormat() Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "Invalid Authorization header format",
		longMessage:  `Invalid Authorization header format. Must be "Bearer <YOUR_API_KEY>"`,
		code:         AuthorizationHeaderFormatInvalidCode,
	})
}

// InvalidAuthorization signifies an error when the request is not authorized to perform the given operation
func InvalidAuthorization() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Unauthorized request",
		longMessage:  "You are not authorized to perform this request",
		code:         AuthorizationInvalidCode,
	})
}

// InvalidCSRFToken signifies an error when the request does not contain a CSRF token or the given token is invalid
func InvalidCSRFToken() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Invalid or missing CSRF token",
		longMessage:  "To protect against CSRF attacks, the given request must include a valid CSRF token.",
		code:         InvalidCSRFTokenCode,
	})
}

func OriginAndAuthorizationMutuallyExclusive() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Setting both the 'Origin' and 'Authorization' headers is forbidden",
		longMessage:  "For security purposes, only one of the 'Origin' and 'Authorization' headers should be provided, but not both. In browser contexts, the 'Origin' header is set automatically by the browser. In native application contexts (e.g. mobile apps), set the 'Authorization' header.",
		code:         OriginAndAuthorizationHeadersSetCode,
	})
}

func MultipleOriginHeaderValues() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Multiple 'Origin' header values",
		longMessage:  "Setting multiple values in the 'Origin' header is forbidden",
		code:         MultipleOriginHeaderValuesCode,
	})
}

func MultipleAuthorizationHeaderValues() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Multiple 'Authorization' header values",
		longMessage:  "Setting multiple values in the 'Authorization' header is forbidden",
		code:         MultipleAuthorizationHeaderValuesCode,
	})
}

// MissingRequestHeaders signifies an error when the incoming request is missing mandatory headers
func MissingRequestHeaders(clientType client_type.ClientType) Error {
	longMessageForStandardBrowsers := "Your Clerk Frontend API is accessible from browsers and native applications. To protect against standard web attacks, the HTTP Origin header is required in browser requests. If you see this error, you probably accessed Clerk Frontend API directly from the address bar or a browser extension is intercepting your browser requests, removing the HTTP Origin header. For more information refer to https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Origin."

	longMessageForNonStandardBrowsers := "Your Clerk Frontend API is accessible from browsers and native applications. To protect against common web attacks, we require the HTTP Authorization header to be present in native application requests. Make sure the HTTP Authorization header is set a valid Clerk client JWT or set it to an empty string for your first Frontend API request that will return your Clerk client JWT."

	longMessage := longMessageForStandardBrowsers
	if clientType.IsNative() {
		longMessage = longMessageForNonStandardBrowsers
	}

	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid request headers",
		longMessage:  longMessage,
		code:         RequestHeaderMissingCode,
	})
}

// InvalidOriginHeader signifies an error when the origin header of the incoming request is invalid
func InvalidOriginHeader() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid HTTP Origin header",
		longMessage:  "The Request HTTP Origin header must be equal to or a subdomain of the requesting URL.",
		code:         OriginInvalidCode,
	})
}

// DevBrowserUnauthenticated signifies an error when the dev browser is not authenticated
func DevBrowserUnauthenticated() Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "Browser unauthenticated",
		longMessage:  "Unable to authenticate this browser for your development instance. Check your Clerk cookies and try again. If the issue persists reach out to support@clerk.com.",
		code:         DevBrowserUnauthenticatedCode,
	})
}

// URLBasedSessionSyncingDisabled signifies an error when the incoming request attempts
// to use an endpoint with URL-based session syncing, when the instance operates with
// third-party cookies instead.
func URLBasedSessionSyncingDisabled() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "URL-based session syncing is disabled for this instance",
		longMessage:  "This is a development instance operating with legacy, third-party cookies. To enable URL-based session syncing refer to https://clerk.com/docs/upgrade-guides/url-based-session-syncing.",
		code:         URLBasedSessionSyncingDisabledCode,
	})
}

// InvalidRequestForEnvironment signifies an error when the incoming request is invalid for given environment(s)
func InvalidRequestForEnvironment(envTypesAsString ...string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid request for environment",
		longMessage:  fmt.Sprintf("Request only valid for %s instances.", strings.Join(envTypesAsString, ", ")),
		code:         RequestInvalidForEnvironmentCode,
	})
}

// RequestInvalidForInstance signifies an error when the incoming request is invalid for the given instance, due to the auth_config
func RequestInvalidForInstance() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid request for instance",
		longMessage:  "This request is not valid for your instance. Modify your instance settings to use this request.",
		code:         RequestInvalidForInstanceCode,
	})
}

// InvalidHost signifies an error when the incoming request has an invalid host
func InvalidHost() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid host",
		longMessage:  "We were unable to attribute this request to an instance running on Clerk. Make sure that your Clerk Publishable Key is correct.",
		code:         HostInvalidCode,
	})
}

// IdentificationExists signifies an error when the identifier already exists
func IdentificationExists(identType string, cause error) Error {
	var code string
	var identifier string

	switch identType {
	case constants.ITEmailAddress:
		code = EmailAddressExistsCode
		identifier = "email address"
	case constants.ITPhoneNumber:
		code = PhoneNumberExistsCode
		identifier = "phone number"
	case constants.ITUsername:
		code = UsernameExistsCode
		identifier = "username"
	case constants.ITSAML:
		code = ExternalAccountExistsCode
		identifier = "SAML account"
	default:
		if oauth.ProviderExists(identType) {
			code = ExternalAccountExistsCode
			identifier = "external account"
		}
	}

	return New(http.StatusBadRequest, &mainError{
		shortMessage: "already exists",
		longMessage:  fmt.Sprintf("This %s already exists.", identifier),
		code:         code,
		cause:        cause,
	})
}

func IdentifierNotAllowedAccess(identifiers ...string) Error {
	who := "You"
	verb := "are"
	if len(identifiers) > 0 {
		who = strings.Join(identifiers, ", ")
	}
	if len(identifiers) == 1 {
		verb = "is"
	}
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Access not allowed.",
		longMessage:  fmt.Sprintf("%s %s not allowed to access this application.", who, verb),
		code:         IdentifierNotAllowedAccessCode,
		meta:         identifiersMeta{Identifiers: identifiers},
	})
}

func BlockedCountry(iso3166 phonenumber.ISO3166) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Country blocked",
		longMessage:  fmt.Sprintf("Phone numbers from this country (%s) are not allowed.", iso3166.CountryName),
		code:         BlockedCountryCode,
		meta:         blockedCountryMeta{Alpha2: iso3166.Alpha2, CountryCode: iso3166.CountryCode},
	})
}

// SignedOut signifies an error when a user is signed out
func SignedOut() Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "Signed out",
		longMessage:  "You are signed out",
		code:         SignedOutCode,
	})
}

// InvalidUserSettings signifies an error where the auth settings of the instance
// are not well configured, which results in sign in and sign up endpoints to be
// restricted.
func InvalidUserSettings() Error {
	return New(http.StatusConflict, &mainError{
		shortMessage: "invalid auth configuration",
		longMessage:  "The authentication settings are invalid.",
		code:         InvalidUserSettingsCode,
	})
}

func InvalidHandshake(reason string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "invalid handshake",
		longMessage:  fmt.Sprintf("The handshake request is invalid: %s", reason),
		code:         InvalidHandshakeCode,
	})
}
