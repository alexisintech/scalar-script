package apierror

import (
	"fmt"
	"net/http"
)

func FormInvalidEntitlementKey(param, value string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "invalid key format",
		longMessage: fmt.Sprintf("%s cannot be used as an entitlement key. An entitlement key should have the following format: <scope>:<feature>. "+
			"Both `scope` and `feature` should only contain alphanumeric characters without any spaces.", value),
		code: FormInvalidEntitlementKeyCode,
		meta: &formParameter{Name: param},
	})
}

func EntitlementAlreadyAssociatedWithProduct() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "already associated",
		longMessage:  "The given entitlement is already associated with the product.",
		code:         EntitlementAlreadyAssociatedCode,
	})
}
