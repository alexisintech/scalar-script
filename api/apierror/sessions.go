package apierror

import (
	"fmt"
	"net/http"
)

// SessionNotFound signifies an error when no session with given sessionID was found
func SessionNotFound(sessionID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Session not found",
		longMessage:  "No session was found with id " + sessionID,
		code:         ResourceNotFoundCode,
	})
}

// InvalidActionForSession signifies an error occurred when user tries to perform invalid action on a session
func InvalidActionForSession(sessionID string, action string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid action for user session",
		longMessage:  fmt.Sprintf("Unable to %s session %s", action, sessionID),
		code:         InvalidActionForSessionCode,
	})
}

// UnauthorizedActionForSession signifies an error occurred when the requestor is not authorized to perform the
// requested action to the respective session.
func UnauthorizedActionForSession(sessionID string) Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "Unauthorized action for session",
		longMessage:  "Not authorized to perform requested action on session " + sessionID,
		code:         UnauthorizedActionForSessionCode,
	})
}

func InvalidSessionToken() Error {
	return New(http.StatusBadRequest,
		&mainError{
			shortMessage: "Invalid session token",
			longMessage:  "The token provided could not be successfully verified",
			code:         InvalidSessionTokenCode,
		})
}

func MissingConfigurableSessionLifetimeOption() Error {
	return New(http.StatusUnprocessableEntity,
		&mainError{
			shortMessage: "Missing session lifetime settings",
			longMessage:  "You must enable at least one of the session lifetime settings",
			code:         MissingSessionLifetimeSettingCode,
		})
}

func CannotCreateSessionWhenImpersonationIsPresent() Error {
	return New(http.StatusConflict,
		&mainError{
			shortMessage: "unable to create session",
			longMessage:  "Unable to create new session when an impersonation session is present. Please sign out first.",
			code:         SessionCreationNotAllowedCode,
		})
}
