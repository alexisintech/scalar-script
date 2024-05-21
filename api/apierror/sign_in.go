package apierror

import (
	"fmt"
	"net/http"
)

// SingleModeSessionExists signifies an error when session already exists but we are in single session mode
func SingleModeSessionExists() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Session already exists",
		longMessage:  "You're currently in single session mode. You can only be signed into one account at a time.",
		code:         SessionExistsCode,
	})
}

// AlreadySignedIn signifies an error when given session ID is already signed in
func AlreadySignedIn(sessionID string) Error {
	session := sessionMeta{SessionID: sessionID}

	return New(http.StatusBadRequest, &mainError{
		shortMessage: "You're already signed in",
		code:         IdentifierAlreadySignedInCode,
		meta:         session,
	})
}

// AccountTransferInvalid signifies an error when no account was found to transfer
func AccountTransferInvalid() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid account transfer",
		longMessage:  "There is no account to transfer",
		code:         AccountTransferInvalidCode,
	})
}

// InvalidClientStateForAction signifies an error when trying to perform an invalid action for the current client state
func InvalidClientStateForAction(action string, resolution string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid action",
		longMessage:  fmt.Sprintf("We were unable to complete %s for this Client. %s", action, resolution),
		code:         ClientStateInvalid,
	})
}

// InvalidStrategyForUser signifies an error when the supplied verification strategy is not valid for the account
func InvalidStrategyForUser() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid verification strategy",
		longMessage:  "The verification strategy is not valid for this account",
		code:         StrategyForUserInvalidCode,
	})
}

// IdentificationClaimed signifies an error when the requested identification is already claimed by another user
func IdentificationClaimed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Identification claimed by another user",
		longMessage:  "One or more identifiers on this sign up have since been connected to a different User. Please sign up again.",
		code:         IdentificationClaimsCode,
	})
}

// MutationOnOlderSignInNotAllowed signifies an error when trying to mutate an older sign in
func MutationOnOlderSignInNotAllowed() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Update operations are not allowed on older sign ins",
		code:         ResourceForbiddenCode,
	})
}

// UserNotFound signifies an error when no user is found with userID
func SignInNotFound(signInID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  "No sign in was found with id " + signInID,
		code:         ResourceNotFoundCode,
	})
}

// IdentificationBelongsToDifferentUser indicates an error when a user
// is trying to perform an operation (e.g. sign in prepare) with an
// identification that doesn't belong to him.
func IdentificationBelongsToDifferentUser() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "belongs to different user",
		longMessage:  "The given identification belongs to a different user.",
		code:         ResourceForbiddenCode,
	})
}

func NoSecondFactorsForStrategy(strategy string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "no second factors",
		longMessage:  fmt.Sprintf("No second factors were found for strategy %s.", strategy),
		code:         NoSecondFactorsForStrategyCode,
	})
}

func SignInNoIdentificationForUser() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "no identification for user",
		longMessage:  "The given token doesn't have an associated identification for the user who created it.",
		code:         SignInNoIdentificationForUserCode,
	})
}

func SignInIdentificationOrUserDeleted() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "identification or user deleted",
		longMessage:  "Either the user or the selected identification were deleted. Please start over.",
		code:         SignInIdentificationOrUserDeletedCode,
	})
}

func SignInEmailLinkNotSameClient() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "email link sign in cannot be completed",
		longMessage:  "Email link sign in cannot be completed because it originates from a different client",
		code:         SignInEmailLinkNotSameClientCode,
	})
}
