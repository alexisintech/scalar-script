package apierror

import (
	"fmt"
	"net/http"
	"strings"

	cstrings "clerk/pkg/strings"
)

// InstanceNotFound signifies an error when no instance with given instanceID was found
func InstanceNotFound(instanceID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Instance not found",
		longMessage:  "No instance was found with id " + instanceID,
		code:         ResourceNotFoundCode,
	})
}

// ProductionInstanceExists signifies an error when trying to create a production instance
// when there is already one
func ProductionInstanceExists() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "You can only have one production instance.",
		code:         ProductionInstanceExistsCode,
	})
}

// InstanceTypeInvalid signifies an error when a request cannot be applied to the given instance
func InstanceTypeInvalid() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "This request isn't valid for this instance type.",
		code:         InstanceTypeInvalidCode,
	})
}

// InstanceNotLive signifies an error when trying to perform an action that requires a live instance but the instance
// is not already live
func InstanceNotLive() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Instance is not live yet",
		longMessage:  "This instance is not live yet. This operation is only available for live instances.",
		code:         InstanceNotLiveCode,
	})
}

// MissingCustomOauthConfig signifies an error when a production instance has
// SSO enabled for a specific OAuth external provider but hasn't setup a custom
// profile yet.
func MissingCustomOauthConfig(oauthProviderID string) Error {
	// TODO(oauth): Remove this when we unify sso provider IDs (facebook vs oauth_facebook)
	oauthProviderID = strings.TrimPrefix(oauthProviderID, "oauth_")

	code := oauthProviderID + OAuthCustomProfileMissingCode
	providerTitle := cstrings.Title(oauthProviderID)

	return New(http.StatusBadRequest, &mainError{
		shortMessage: fmt.Sprintf("%v dedicated OAuth Client IDs is missing.", providerTitle),
		longMessage:  fmt.Sprintf("Production instances should use their own %v OAuth Client ID and secret. Please update your user management SSO settings accordingly.", providerTitle),
		code:         code,
	})
}

// DevelopmentInstanceMissing
func DevelopmentInstanceMissing(appID string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Development instance missing",
		longMessage:  fmt.Sprintf("No development instance found for application_id: %s", appID),
		code:         DevelopmentInstanceMissingCode,
	})
}

// BreaksInstanceInvariantCode
func BreaksInstanceInvariant(invariantDescription string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Breaks instance invariant",
		longMessage:  fmt.Sprintf("%v - This invariant is determined by your user settings", invariantDescription),
		code:         BreaksInstanceInvariantCode,
	})
}

func CannotDeleteActiveProductionInstance() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "cannot delete active production instance",
		longMessage: "The selected production instance cannot be deleted because it had activity in the last month. " +
			"If you are certain you want to delete it, please contact support.",
		code: ActiveProductionInstanceDeletionNotAllowedCode,
	})
}
