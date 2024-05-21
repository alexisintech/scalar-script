package apierror

import "net/http"

// VerificationAlreadyVerified signifies an error when verification has already been verified
func VerificationAlreadyVerified() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "already verified",
		longMessage:  "This verification has already been verified.",
		code:         VerificationAlreadyVerifiedCode,
	})
}

// VerificationExpired signifies an error when verification has expired
func VerificationExpired() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "expired",
		longMessage:  "This verification has expired. You must create a new one.",
		code:         VerificationExpiredCode,
	})
}

// VerificationFailed signifies an error when verification fails
func VerificationFailed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "failed",
		longMessage:  "Too many failed attempts. You have to try again with the same or another method.",
		code:         VerificationFailedCode,
	})
}

// VerificationInvalidStrategy signifies an error when the given strategy is not valid for current verification
func VerificationInvalidStrategy() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "has invalid strategy",
		longMessage:  "The strategy is not valid for the current verification.",
		code:         VerificationStrategyInvalidCode,
	})
}

// VerificationMissing signifies an error when the verification is missing
func VerificationMissing() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "is missing",
		longMessage:  "This strategy requires verification preparation before attempting to validate it.",
		code:         VerificationMissingCode,
	})
}

// VerificationNotSent signifies an error when verification email was not sent
func VerificationNotSent() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "not sent",
		longMessage:  "You need to send a verification code before attempting to verify.",
		code:         VerificationNotSentCode,
	})
}

// VerificationUnknownStatus signifies an unexpected error when unknown verification status is found
func VerificationUnknownStatus(status string) Error {
	return New(http.StatusInternalServerError, &mainError{
		shortMessage: "Unknown verification status",
		longMessage:  "Found unknown verification status " + status,
		code:         VerificationStatusUnknownCode,
	})
}

// VerificationInvalidLinkToken means that the provided JWT token from the
// link cannot be parsed.
func VerificationInvalidLinkToken() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "invalid link token",
		longMessage:  "Verification link token is invalid",
		code:         VerificationInvalidLinkTokenCode,
	})
}

// VerificationLinkTokenExpired means that the provided JWT token from the
// link has expired.
func VerificationLinkTokenExpired() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "expired link token",
		longMessage:  "Verification link token has expired",
		code:         VerificationLinkTokenExpiredCode,
	})
}

// VerificationInvalidLinkTokenSource means that the provided JWT token from
// the link has an invalid source type.
func VerificationInvalidLinkTokenSource() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "invalid link token source",
		longMessage:  "Verification link token source is invalid",
		code:         VerificationInvalidLinkTokenSourceCode,
	})
}
