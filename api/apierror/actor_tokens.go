package apierror

import (
	"fmt"
	"net/http"
)

func ActorTokenRevoked() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "actor token has been revoked",
		longMessage:  "This actor token has been revoked and cannot be used anymore.",
		code:         ActorTokenRevokedCode,
	})
}

func ActorTokenAlreadyUsed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "actor token has already been used",
		longMessage:  "This actor token has already been used. Each token can only be used once.",
		code:         ActorTokenAlreadyUsedCode,
	})
}

func ActorTokenCannotBeUsed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "actor token cannot be used",
		longMessage:  "This actor token cannot be used anymore. Please request a new one.",
		code:         ActorTokenCannotBeUsedCode,
	})
}

func ActorTokenCanBeUsedOnlyInSignIn() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "not in sign in",
		longMessage:  "Actor tokens can only be used during sign in.",
		code:         ActorTokenNotInSignInCode,
	})
}

func ActorTokenSubjectNotFound() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "user not found",
		longMessage:  "The user of the actor token no longer exists. Please request a new one.",
		code:         ActorTokenSubjectNotFoundCode,
	})
}

func ActorTokenCannotBeRevoked(status string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "cannot revoke",
		longMessage:  fmt.Sprintf("Actor token cannot be revoked because its status is %s. Only pending tokens can be revoked.", status),
		code:         ActorTokenCannotBeRevokedCode,
	})
}
