package apierror

import (
	"net/http"
)

func SystemUnderMaintenance() Error {
	return New(http.StatusServiceUnavailable, &mainError{
		shortMessage: "System under maintenance",
		longMessage:  "We are currently undergoing maintenance and only essential operations are permitted. We will be back shortly.",
		code:         MaintenanceModeCode,
	})
}
