package apierror

import (
	"fmt"
	"net/http"
)

func PasskeyRegistrationFailure() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Passkey registration failed",
		longMessage:  "Passkey registration flow could not be completed",
		code:         PasskeyRegistrationFailureCode,
	})
}

func NoPasskeysFoundForUser() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "User has no passkeys",
		longMessage:  "User has no passkeys registered for this account",
		code:         NoPasskeysFoundForUserCode,
	})
}

func PasskeyNotRegistered() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not registered",
		longMessage:  "Passkey is not registered.",
		code:         PasskeyNotRegisteredCode,
	})
}

func PasskeyIdentificationNotVerified() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "passkey identification not verified",
		longMessage:  "Passkey identification not verified. Registration is incomplete.",
		code:         PasskeyIdentificationNotVerifiedCode,
	})
}

func PasskeyInvalidPublicKeyCredential(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  "Invalid passkey public key credential",
		code:         PasskeyInvalidPublicKeyCredentialCode,
		meta:         &formParameter{Name: param},
	})
}

func PasskeyInvalidVerification() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "invalid verification",
		longMessage:  "Passkey verification contains invalid nonce",
		code:         PasskeyInvalidVerificationCode,
	})
}

func PasskeyAuthenticationFailure() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "authentication failed",
		longMessage:  "Passkey authentication failed",
		code:         PasskeyAuthenticationFailureCode,
	})
}

func PasskeyQuotaExceeded(maxAllowed int) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "passkey quota exceeded",
		longMessage:  fmt.Sprintf("You have reached your limit of %d passkeys per account.", maxAllowed),
		code:         PasskeyQuotaExceededCode,
	})
}
