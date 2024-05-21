package apierror

import "net/http"

func URLNotFound() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "URL not found",
		longMessage:  "The URL was not found",
		code:         ResourceNotFoundCode,
	})
}
