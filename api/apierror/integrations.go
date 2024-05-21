package apierror

import (
	"fmt"
	"net/http"
)

// IntegrationNotFound signifies an error when no integration with given integrationID was found
func IntegrationNotFound(integrationID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Integration not found",
		longMessage:  "No integration was found with id " + integrationID,
		code:         ResourceNotFoundCode,
	})
}

// IntegrationNotFoundByType signifies an error when no integration with given type was found for an instance
func IntegrationNotFoundByType(instanceID, integrationType string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Integration not found",
		longMessage:  fmt.Sprintf("No integration with type %s found for instance_id: %s", integrationType, instanceID),
		code:         ResourceNotFoundCode,
	})
}

// IntegrationOauthFailure signifies an error in completing the oouth flow necessary for a certain integration
func IntegrationOauthFailure() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Integration oauth flow could not be completed",
		longMessage:  "Could not obtain an oauth token necessary for the current integration",
		code:         IntegrationOauthFailureCode,
	})
}

// IntegrationTokenMissing signifies that a token necessary for the integration to work is missing
func IntegrationTokenMissing(integrationID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Integration token missing",
		longMessage:  fmt.Sprintf("No corresponding third party tokens found for integration_id: %s", integrationID),
		code:         IntegrationTokenMissingCode,
	})
}

// IntegrationUserInfoError signifies that there was an error retrieving user info for this integration
func IntegrationUserInfoError(integrationID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "User info retrieval error",
		longMessage:  fmt.Sprintf("Could not retrieve user info for integration_id: %s", integrationID),
		code:         IntegrationUserInfoErrorCode,
	})
}

// IntegrationProvisioningFailed
func IntegrationProvisioningFailed(integrationID string, projectID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Integration provisioning failed",
		longMessage:  fmt.Sprintf("Failed to provision Vercel project_id: %s for integration_id: %s", projectID, integrationID),
		code:         IntegrationProvisioningFailedCode,
	})
}

// UnsupportedIntegrationType
func UnsupportedIntegrationType(integrationType string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Unsupported integration type",
		longMessage:  fmt.Sprintf("Unsupported integration type: %s", integrationType),
		code:         UnsupportedIntegrationTypeCode,
	})
}
