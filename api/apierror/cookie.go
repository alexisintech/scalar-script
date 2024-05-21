package apierror

import (
	"net/http"

	"clerk/pkg/clerkerrors"
)

const (
	invalidCookieMessage = "The provided cookie is invalid."
)

// MissingClaims signifies an error when token is missing claim
func MissingClaims(claims string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: invalidCookieMessage,
		longMessage:  "The token is missing the following claims:" + claims,
		code:         CookieInvalidCode,
	})
}

// InvalidCookie signifies an error when cookie is invalid
func InvalidCookie(err error) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: invalidCookieMessage,
		code:         CookieInvalidCode,
		cause:        clerkerrors.Wrap(err, 1),
	})
}

// InvalidRotatingToken signifies an error when rotating token does not match the client's rotating token
func InvalidRotatingToken(token string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: invalidCookieMessage,
		longMessage:  "The client's rotating key does not match the given one " + token,
		code:         CookieInvalidCode,
	})
}
