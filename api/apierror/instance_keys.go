package apierror

import (
	"fmt"
	"net/http"
)

// LastInstanceKey signifies an error when there is an attempt to delete the last key for an instance
func LastInstanceKey(instanceID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Cannot delete last key for instance",
		longMessage:  fmt.Sprintf("Cannot delete last key for instance %s", instanceID),
		code:         LastInstanceKeyCode,
	})
}

// InstanceKeyRequired signifies an error when no instance keys exist
func InstanceKeyRequired() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Key required",
		longMessage:  "Please generate at least one instance key",
		code:         InstanceKeyRequiredCode,
	})
}
