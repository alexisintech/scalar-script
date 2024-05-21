package apierror

import "net/http"

func DashboardMutationsDuringImpersonationForbidden() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "not allowed",
		longMessage:  "Dashboard mutations are not allowed during impersonation",
		code:         FormParamMissingCode,
	})
}
