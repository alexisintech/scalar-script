package apierror

import "net/http"

// ClientNotFound signifies an error when no client is found with clientID
func ClientNotFound(clientID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Client not found",
		longMessage:  "No client was found with id " + clientID,
		code:         ResourceNotFoundCode,
	})
}

// ClientNotFoundInRequest signifies an error when no client is found in an incoming request
func ClientNotFoundInRequest() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "No client found",
		longMessage:  "This request is expecting a client and did not find one",
		code:         ClientNotFoundCode,
	})
}
