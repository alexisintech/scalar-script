package apierror

import (
	"fmt"
	"net/http"
)

func DuplicateBlocklistIdentifier(identifier string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "duplicate blocklist identifier",
		longMessage:  fmt.Sprintf("the identifier %s already exists", identifier),
		code:         DuplicateRecordCode,
	})
}

func BlocklistIdentifierNotFound(identifierID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Identifier not found",
		longMessage:  "No identifier was found with id " + identifierID,
		code:         ResourceNotFoundCode,
	})
}
