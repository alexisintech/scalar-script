package apierror

import (
	"fmt"
	"net/http"
)

// OAuthFetchUserInfo signifies an error when user info cannot be retrieved with the access token
func OAuthFetchUserInfo() Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "unable to fetch user info",
		longMessage:  "Unable to fetch user info. Check if access token is present and valid.",
		code:         OAuthFetchUserErrorCode,
	})
}

// OAuthInvalidAuthorizeRequest signifies an error with the /oauth/authorize request that prevents redirection
func OAuthAuthorizeRequestError(statusCode int, errorCode string, errorDescription string) Error {
	return New(statusCode, &mainError{
		shortMessage: errorCode,
		longMessage:  fmt.Sprintf("%s: %s", errorDescription, errorCode),
		code:         OAuthAuthorizeRequestErrorCode,
	})
}

// OAuthFetchUserInfoForbidden signifies an error when user info request is denied
func OAuthFetchUserInfoForbidden() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "unable to fetch user info",
		longMessage:  "Unable to fetch user info. User is not allowed access to this application.",
		code:         OAuthFetchUserForbiddenErrorCode,
	})
}
