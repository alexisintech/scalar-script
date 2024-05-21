package apierror

import "net/http"

func BAPIEndpointDeprecated(message string) Error {
	return New(http.StatusGone, &mainError{
		shortMessage: "endpoint is deprecated and pending removal",
		longMessage:  message,
		code:         APIOperationDeprecatedCode,
	})
}
