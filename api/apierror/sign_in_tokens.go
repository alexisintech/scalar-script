package apierror

import (
	"fmt"
	"net/http"
)

func SignInTokenRevoked() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "sign in token has been revoked",
		longMessage:  "This sign in token has been revoked and cannot be used anymore.",
		code:         SignInTokenRevokedCode,
	})
}

func SignInTokenAlreadyUsed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "sign in token has already been used",
		longMessage:  "This sign in token has already been used. Each token can only be used once.",
		code:         SignInTokenAlreadyUsedCode,
	})
}

func SignInTokenCannotBeUsed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "sign in token cannot be used",
		longMessage:  "This sign in token cannot be used anymore. Please request a new one.",
		code:         SignInTokenCannotBeUsedCode,
	})
}

func SignInTokenCanBeUsedOnlyInSignIn() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "not in sign in",
		longMessage:  "Sign in tokens can only be used during sign in.",
		code:         SignInTokenNotInSignInCode,
	})
}

func SignInTokenCannotBeRevoked(status string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "cannot revoke",
		longMessage:  fmt.Sprintf("Sign in token cannot be revoked because its status is %s. Only pending tokens can be revoked.", status),
		code:         SignInTokenCannotBeRevokedCode,
	})
}
