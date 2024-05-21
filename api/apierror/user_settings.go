package apierror

import "net/http"

func ResourceInvalid(msg string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Resource invalid",
		longMessage:  msg,
		code:         ResourceInvalidCode,
	})
}

func ResourceNotFound() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  "Resource not found",
		code:         ResourceNotFoundCode,
	})
}

func ResourceForbidden() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "forbidden",
		longMessage:  "Resource forbidden",
		code:         ResourceForbiddenCode,
	})
}
