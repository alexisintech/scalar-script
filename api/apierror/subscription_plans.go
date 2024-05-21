package apierror

import (
	"fmt"
	"net/http"
)

func ProductNotSupportedBySubscriptionPlan(productID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Product not supported by subscription plan",
		longMessage:  fmt.Sprintf("The product %s is not compatible with the current subscription plan", productID),
		code:         ProductNotSupportedBySubscriptionPlanCode,
	})
}

func ProductAlreadySubscribed(productID string) Error {
	return New(http.StatusConflict, &mainError{
		shortMessage: "Product already subscribed",
		longMessage:  fmt.Sprintf("Product %s is already enabled for the current subscription.", productID),
		code:         ProductAlreadySubscribedCode,
	})
}

func InactiveSubscription(id string) Error {
	return New(http.StatusGone, &mainError{
		shortMessage: "Inactive subscription",
		longMessage:  fmt.Sprintf("Subscription %s is not active or trialling", id),
		code:         InactiveSubscriptionCode,
	})
}
