package apierror

import "net/http"

// GatewayTimeout signifies an error when a 3rd party service takes too long to respond.
func GatewayTimeout() Error {
	return New(http.StatusGatewayTimeout, &mainError{
		shortMessage: "Gateway Timeout",
		longMessage:  "A request to a 3rd party service timed out",
		code:         GatewayTimeoutCode,
	})
}
