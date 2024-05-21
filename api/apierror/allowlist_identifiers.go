package apierror

import (
	"fmt"
	"net/http"
)

func AllowlistIdentifierNotFound(identifierID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Identifier not found",
		longMessage:  "No identifier was found with id " + identifierID,
		code:         ResourceNotFoundCode,
	})
}

func DuplicateAllowlistIdentifier(identifier string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "duplicate allowlist identifier",
		longMessage:  fmt.Sprintf("the identifier %s already exists", identifier),
		code:         DuplicateRecordCode,
	})
}
