package apierror

import (
	"fmt"
	"net/http"
)

func SvixAppAlreadyExists() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Only one Svix app is allowed per instance.",
		code:         SvixAppExistsCode,
	})
}

func SvixAppMissing() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "No Svix apps are associated with the current instance.",
		code:         SvixAppMissingCode,
	})
}

func SvixAppCreateError(name string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Svix app creation failed",
		longMessage:  fmt.Sprintf("Could not create a Svix app with name %s at this time. Please contact us if this error persists.", name),
		code:         SvixAppCreateErrorCode,
	})
}
