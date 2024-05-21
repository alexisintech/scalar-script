package apierror

import (
	"fmt"
	"net/http"
)

// RedirectURLNotFound signifies an error when a RedirectURL was not found by the provided attribute
func RedirectURLNotFound(attribute, val string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Redirect url not found",
		longMessage:  fmt.Sprintf("No RedirectURL exists with %s: %s", attribute, val),
		code:         ResourceNotFoundCode,
	})
}

// RedirectURLMismatch signifies an error when the RedirectURL that was passed during an OAuth flow is not included in the redirect_urls whitelist for that instance.
func RedirectURLMismatch(val string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Redirect url mismatch",
		longMessage:  fmt.Sprintf("The current redirect url passed in the sign in or sign up request does not match an authorized redirect URI for this instance. Review authorized redirect urls for your instance. %s", val),
		code:         ResourceMismatchCode,
	})
}

// InvalidRedirectURL signifies an error when a RedirectURL is in invalid format
func InvalidRedirectURL() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Redirect url invalid",
		longMessage:  "The provided redirect url is not in a valid format",
		code:         InvalidRedirectURLCode,
	})
}
