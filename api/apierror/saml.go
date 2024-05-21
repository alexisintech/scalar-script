package apierror

import (
	"fmt"
	"net/http"
	"strings"

	"clerk/pkg/clerkerrors"
)

func SAMLResponseRelayStateMissing() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "RelayState parameter missing",
		longMessage:  "The RelayState parameter is missing from the SAML Response. Contact your IdP administrator for resolution.",
		code:         SAMLResponseRelayStateMissingCode,
	})
}

func SAMLResponseInvalid(err error) Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "Invalid SAML response",
		longMessage:  "The SAML response is invalid.",
		code:         SAMLResponseInvalidCode,
		cause:        clerkerrors.Wrap(err, 1),
	})
}

func SAMLNotEnabled(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "SAML SSO not enabled",
		longMessage:  "SAML SSO is not enabled for this email address.",
		code:         SAMLNotEnabledCode,
		meta:         &formParameter{Name: param},
	})
}

func SAMLSignInConnectionMissing() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "No SAML Connection for this sign-in",
		longMessage:  "The current sign-in does not have a corresponding SAML Connection.",
		code:         SAMLSignInConnectionMissingCode,
	})
}

func SAMLSignUpConnectionMissing() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "No SAML Connection for this sign-up",
		longMessage:  "The current sign-up does not have a corresponding SAML Connection.",
		code:         SAMLSignUpConnectionMissingCode,
	})
}

func SAMLUserAttributeMissing(attrName string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "SAML SSO user attribute missing",
		longMessage:  fmt.Sprintf("This account does not have an associated '%s' attribute. Contact your IdP administrator for resolution.", attrName),
		code:         SAMLUserAttributeMissingCode,
	})
}

func SAMLEmailAddressDomainMismatch() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Email address domain mismatch",
		longMessage:  "The email address domain of the provider's account does not match the domain of the connection.",
		code:         SAMLEmailAddressDomainMismatchCode,
	})
}

func SAMLConnectionCantBeActivated(missingFields []string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "SAML Connection can't be activated",
		longMessage:  fmt.Sprintf("You have to provide the %s before you are able to activate this connection.", strings.Join(missingFields, ", ")),
		code:         SAMLConnectionCantBeActivatedCode,
	})
}

func SAMLConnectionActiveNotFound(connectionID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  fmt.Sprintf("No active SAML Connection found with id %s.", connectionID),
		code:         SAMLConnectionActiveNotFoundCode,
	})
}

func SAMLFailedToFetchIDPMetadata() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Failed to fetch IdP metadata",
		longMessage:  "We failed to fetch the IdP metadata. If the error persists, please provide the IdP configuration data explicitly.",
		code:         SAMLFailedToFetchIDPMetadataCode,
	})
}

func SAMLFailedToParseIDPMetadata() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Failed to parse IdP metadata",
		longMessage:  "We failed to parse the IdP metadata. If the error persists, please provide the IdP configuration data explicitly.",
		code:         SAMLFailedToParseIDPMetadataCode,
	})
}

func SAMLEmailAddressDomainReserved() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "email address domain is used for SAML SSO",
		longMessage:  "You can't use this email address, as SAML SSO is enabled for the specific domain.",
		code:         SAMLEmailAddressDomainReservedCode,
	})
}
