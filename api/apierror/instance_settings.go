package apierror

import (
	"fmt"
	"net/http"
	"strings"

	"clerk/pkg/constants"
)

func EnhancedEmailDeliverabilityProhibited() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Enhanced email deliverability mode is only compatible with email codes (OTP)",
		longMessage:  "Ensure that either enhanced email deliverability is disabled or you only have email codes (OTP) enabled.",
		code:         EnhancedEmailDeliverabilityProhibitedCode,
	})
}

func InvalidCaptchaWidgetType(widgetType string) Error {
	allowed := []string{}
	for _, t := range constants.TurnstileWidgetTypes.Array() {
		allowed = append(allowed, string(t))
	}

	longmsg := fmt.Sprintf("The captcha widget type '%s' is invalid. Allowed values: %s",
		widgetType, strings.Join(allowed, ", "))

	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Invalid captcha widget type",
		longMessage:  longmsg,
		code:         InvalidCaptchaWidgetTypeCode,
	})
}
