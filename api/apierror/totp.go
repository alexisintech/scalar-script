package apierror

import (
	"net/http"
)

// TOTPAlreadyEnabled signifies an error when a user attempts to enable TOTP, but it's already enabled.
func TOTPAlreadyEnabled() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "TOTP already enabled",
		longMessage:  "TOTP is already enabled on your account",
		code:         TOTPAlreadyEnabledCode,
	})
}
