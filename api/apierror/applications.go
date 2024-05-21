package apierror

import (
	"fmt"
	"net/http"
)

// ApplicationNotFound signifies an error when no application with given appID was found
func ApplicationNotFound(appID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Application not found",
		longMessage:  "No application was found with id " + appID,
		code:         ResourceNotFoundCode,
	})
}

// NotAuthorizedToDeleteSystemApplication signifies an error when trying to delete a system application
func NotAuthorizedToDeleteSystemApplication(applicationID string) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Unauthorized request",
		longMessage:  "You are not authorized to delete system application " + applicationID,
		code:         AuthorizationInvalidCode,
	})
}

// NotAuthorizedToMoveApplicationToOrganization signifies an error when trying to move an application
// to an organization that the requesting user doesn't belong to.
func NotAuthorizedToMoveApplicationToOrganization(applicationID, organizationID string) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "unauthorized request",
		longMessage: fmt.Sprintf("You need to be a member of organization %s, in order to move application %s.",
			organizationID, applicationID),
		code: AuthorizationInvalidCode,
	})
}

func ApplicationAlreadyBelongsToOrganization() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "already belongs to organization",
		longMessage:  "Application already belongs to the selected organization.",
		code:         ApplicationAlreadyBelongsToOrganizationCode,
	})
}

func ApplicationAlreadyBelongsToUser() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "already belongs to user",
		longMessage:  "Application already belongs to the given user.",
		code:         ApplicationAlreadyBelongsToUserCode,
	})
}

// InvalidPlanForResource is returned when an invalid plan is selected for a
// resource.
func InvalidPlanForResource(resourceID, resourceType, planID string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Invalid plan",
		longMessage:  fmt.Sprintf("Plan %s can't be selected for %s %s", planID, resourceType, resourceID),
		code:         InvalidPlan,
	})
}

func CannotTransferPaidAppToAccountWithoutBillingInformation() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "cannot transfer paid application, missing billing info",
		longMessage:  "Paid applications can only be transferred to personal workspaces or organizations with billing info. Add the necessary billing info and try again.",
		code:         TransferPaidAppToFreeAccountCode,
	})
}

func CannotTransferToAccountWithoutPaymentMethod() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "cannot transfer paid application, missing payment method",
		longMessage:  "The selected account doesn't have any payment methods associated with it.",
		code:         TransferPaidAppToAccountWithNoPaymentMethodCode,
	})
}

func CannotDeleteActiveApplication() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "cannot delete active application",
		longMessage: "The selected application cannot be deleted because it had production activity in the last month. " +
			"If you are sure you want to delete it, please contact support.",
		code: ActiveApplicationDeletionNotAllowedCode,
	})
}

func InvalidApplicationName(name string, reason string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "invalid application name",
		longMessage:  fmt.Sprintf("The application name %q is invalid: %s", name, reason),
		code:         FormParamValueInvalidCode,
		meta: &formParameter{
			// NOTE(izaak): This is the json name of the parameter that is invalid.
			// If we ever rename the application name field in the API, this will need to be updated.
			// The dashboard depends on this value being present to know which form field to highlight.
			Name: "name",
		},
	})
}
