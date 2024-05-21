package apierror

import (
	"net/http"

	sdk "github.com/clerk/clerk-sdk-go/v2"
)

// SDKCallFailed signifies an error returned by the server when using the Clerk's SDK
func SDKCallFailed(err *sdk.APIErrorResponse) Error {
	httpCode := http.StatusInternalServerError
	if err.HTTPStatusCode != 0 {
		httpCode = err.HTTPStatusCode
	}
	var apiErr Error
	for _, e := range err.Errors {
		apiErr = Combine(apiErr, New(httpCode, &mainError{
			shortMessage: e.Message,
			longMessage:  e.LongMessage,
			code:         e.Code,
			meta:         e.Meta,
		}))
	}
	return apiErr
}
