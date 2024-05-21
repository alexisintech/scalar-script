package apierror

import (
	"fmt"
	"net/http"
)

func FeatureNotEnabled() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "not enabled",
		longMessage:  "This feature is not enabled on this instance",
		code:         FeatureNotEnabledCode,
	})
}

func NotImplemented(feature string) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "not implemented",
		longMessage:  fmt.Sprintf("Feature `%s` is not available yet", feature),
		code:         FeatureNotImplementedCode,
	})
}

func FeatureRequiresPSU(feature string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "not a Progressive Sign Up instance",
		longMessage: fmt.Sprintf(
			"%s can only be used in instances that migrated to Progressive Sign Up (https://clerk.com/docs/upgrade-guides/progressive-sign-up)", feature),
		code: FeatureRequiresPSUCode,
	})
}
