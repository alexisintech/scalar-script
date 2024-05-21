package apierror

import (
	"fmt"
	"net/http"
	"strings"
)

// IdentificationNotFound signifies an error when comm is not found
func IdentificationNotFound(resourceID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Resource not found",
		longMessage:  "No resource was found for ID " + resourceID,
		code:         ResourceNotFoundCode,
	})
}

// LastIdentificationDeletionFailed signifies an error when trying to delete the last identification associated with a user
func LastIdentificationDeletionFailed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Deletion failed",
		longMessage:  "You cannot delete your last identification.",
		code:         IdentificationDeletionFailedCode,
	})
}

func LastRequiredIdentificationDeletionFailed(identType string) Error {
	sanitizedIdentType := strings.ReplaceAll(identType, "_", " ")

	return New(http.StatusBadRequest, &mainError{
		shortMessage: fmt.Sprintf("Deleting your last %s is prohibited", sanitizedIdentType),
		longMessage:  fmt.Sprintf("You are required to maintain at least one %s in your account at all times", sanitizedIdentType),
		code:         LastRequiredIdentificationDeletionFailedCode,
	})
}

func LastIdentificationSetFor2FAFailed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Update failed",
		longMessage:  "You cannot set your last identification as second factor.",
		code:         IdentificationSetFor2FAFailedCode,
	})
}

func UpdateSecondFactorUnverified() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Update failed",
		longMessage:  "Cannot update second factor attributes for unverified identification",
		code:         IdentificationUpdateSecondFactorUnverified,
	})
}

func CreateSecondFactorUnverified() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Create failed",
		longMessage:  "Unverified identifications cannot be a second factor",
		code:         IdentificationCreateSecondFactorUnverified,
	})
}

func TooManyUnverifiedIdentifications() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "too many unverified contacts",
		longMessage:  "There are too many unverified contacts for this user.",
		code:         TooManyUnverifiedIdentificationsCode,
	})
}

func PrimaryIdentifierNotFound(userID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Identification not found",
		longMessage:  fmt.Sprintf("No primary identification was found for user %s", userID),
		code:         PrimaryIdentificationNotFoundCode,
	})
}
