package apierror

import (
	"fmt"
	"net/http"
)

// SignUpNotFound returns an API error where no sign up could be found with
// the requested ID.
func SignUpNotFound(id string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Sign up not found",
		longMessage:  fmt.Sprintf("No sign up was found with id %s", id),
		code:         ResourceNotFoundCode,
	})
}

func SignUpForbiddenAccess() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Sign up forbidden",
		longMessage:  "Access to this sign up is forbidden",
		code:         ResourceForbiddenCode,
	})
}

func SignUpCannotBeUpdated() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Sign up cannot be updated",
		longMessage:  "This sign up has reached a terminal state and cannot be updated",
		code:         SignUpCannotBeUpdatedCode,
	})
}

func CaptchaNotEnabled() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "CAPTCHA not enabled",
		longMessage:  "Bot detection can be applied only for production instances which have enabled CAPTCHA.",
		code:         CaptchaNotEnabledCode,
	})
}

func CaptchaUnsupportedByClient(supportEmail *string) Error {
	support := "support"
	if supportEmail != nil {
		support = *supportEmail
	}

	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Cannot perform CAPTCHA challenge.",
		longMessage:  "This application requires a Bot Protection challenge, which is only supported by standard web browser environments. It seems you are using a non-standard client (e.g. native/mobile). Please contact " + support + ".",
		code:         CaptchaNotSupportedByClient,
	})
}

func CaptchaInvalid() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Invalid token",
		code:         CaptchaInvalidCode,
	})
}

func SignUpOutdatedVerification() Error {
	return New(http.StatusGone, &mainError{
		shortMessage: "Outdated verification",
		longMessage:  "There is a more recent verification pending for this signup. Try attempting the verification again.",
		code:         SignUpOutdatedVerificationCode,
	})
}

func SignUpEmailLinkNotSameClient() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "email link sign up cannot be completed",
		longMessage:  "Email link sign up cannot be completed because it originates from a different client",
		code:         SignUpEmailLinkNotSameClientCode,
	})
}
