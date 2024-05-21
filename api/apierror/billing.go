package apierror

import (
	"fmt"
	"net/http"
)

func CheckoutLocked(appID string) Error {
	return New(http.StatusLocked, &mainError{
		shortMessage: "Checkout is still processing",
		longMessage:  fmt.Sprintf("Checkout is still processing for application ID %s", appID),
		code:         CheckoutLockedCode,
	})
}

func CheckoutSessionMismatch(appID, checkoutSessionID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Checkout session ID mismatch",
		longMessage:  fmt.Sprintf("Application ID %s has no matching checkout session ID %s", appID, checkoutSessionID),
		code:         CheckoutSessionMismatchCode,
	})
}

func UnsupportedSubscriptionPlanFeatures(unsupportedFeatures []string) Error {
	return New(http.StatusPaymentRequired, &mainError{
		shortMessage: "Unsupported plan features",
		longMessage:  "Some features are not supported in your current plan. Upgrade your subscription to unlock them.",
		code:         UnsupportedSubscriptionPlanFeaturesCode,
		meta: &unsupportedSubscriptionParams{
			UnsupportedFeatures: unsupportedFeatures,
		},
	})
}

func InvalidSubscriptionPlanSwitch(unsupportedFeatures []string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Unsupported plan features",
		longMessage:  "Some application features are not supported in your new plan. Stay with your current plan to avoid breaking changes.",
		code:         InvalidSubscriptionPlanSwitchCode,
		meta: &unsupportedSubscriptionParams{
			UnsupportedFeatures: unsupportedFeatures,
		},
	})
}

func NoBillingAccountConnectedToInstance() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "no billing account",
		longMessage:  "No billing account is connected to the given instance. Please go via the connect flow first.",
		code:         NoBillingAccountConnectedCode,
	})
}

func BillingCheckoutSessionNotFound(checkoutSessionID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Checkout session ID not found",
		longMessage:  fmt.Sprintf("Checkout session ID %s not found", checkoutSessionID),
		code:         BillingCheckoutSessionNotFoundCode,
	})
}

func BillingCheckoutSessionAlreadyProcessed(checkoutSessionID string) Error {
	return New(http.StatusConflict, &mainError{
		shortMessage: "Checkout session already processed",
		longMessage:  fmt.Sprintf("Checkout session ID %s already processed", checkoutSessionID),
		code:         BillingCheckoutSessionAlreadyProcessedCode,
	})
}

func BillingCheckoutSessionNotCompleted(checkoutSessionID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Checkout session not completed",
		longMessage:  fmt.Sprintf("Checkout session ID %s not completed", checkoutSessionID),
		code:         BillingCheckoutSessionNotCompletedCode,
	})
}

func BillingPlanAlreadyActive() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Plan already active",
		longMessage:  "The requested plan is already active",
		code:         BillingPlanAlreadyActiveCode,
	})
}

func CustomPlanAlreadyExists() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Plan already exists",
		code:         PricingPlanAlreadyExistsCode,
	})
}
