package apierror

import (
	"fmt"
	"net/http"
)

// JWTTemplateNotFound signifies an error when a JWT template was not found by the provided attribute
func JWTTemplateNotFound(attribute, val string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "JWT template not found",
		longMessage:  fmt.Sprintf("No JWT template exists with %s: %s", attribute, val),
		code:         ResourceNotFoundCode,
	})
}

// JWTTemplateReservedClaim denotes an error when the provided template contains a reserved claim.
func JWTTemplateReservedClaim(param, claim string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "reserved claim used",
		longMessage:  fmt.Sprintf("You can't use the reserved claim: '%s'", claim),
		code:         JWTTemplateReservedClaimCode,
		meta:         &formParameter{Name: param},
	})
}

func SessionTokenTemplateNotDeletable() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "session token template cannot be deleted",
		longMessage:  "This template cannot be deleted because it's a session token template",
		code:         SessionTokenTemplateNotDeletableCode,
	})
}
