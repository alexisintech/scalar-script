package apierror

import (
	"fmt"
	"net/http"
)

// DevMonthlySMSLimitExceeded signifies an error when an SMS sending attempt is made while the development limit has already been reached
func DevMonthlySMSLimitExceeded(limit int) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Development monthly SMS limit exceeded",
		longMessage:  fmt.Sprintf("Operation cannot be completed because the monthly limit for SMS messages in development (%d) has been reached.", limit),
		code:         DevMonthlySMSLimitExceededCode,
		meta: devLimits{
			limit,
		},
	})
}
