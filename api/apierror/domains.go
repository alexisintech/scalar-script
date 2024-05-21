package apierror

import (
	"net/http"
)

// DomainNotFound signifies an error when no domain with the given id was found
func DomainNotFound(id string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Domain not found",
		longMessage:  "No domain was found with " + id,
		code:         ResourceNotFoundCode,
	})
}

// DomainUpdateForbidden signifies an error when trying to update an non production instance domain
func DomainUpdateForbidden() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Domain update was forbidden",
		longMessage:  "Domain can be only updated for production instances",
		code:         DomainUpdateForbiddenCode,
	})
}

func OperationNotAllowedOnSatelliteDomain() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "operation not allowed",
		longMessage:  "This operation is not allowed on a satellite domain. Try again using the primary domain of your instance.",
		code:         OperationNotAllowedOnSatelliteDomainCode,
	})
}

func OperationNotAllowedOnPrimaryDomain() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "operation not allowed",
		longMessage:  "This operation is not allowed on a primary domain. Try again with a satellite domain of the instance.",
		code:         OperationNotAllowedOnPrimaryDomainCode,
	})
}

// SyncNonceAlreadyConsumed signifies an error when the nonce that was given
// during the sync flow is already consumed.
func SyncNonceAlreadyConsumed() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "sync nonce already consumed",
		longMessage:  "The given sync nonce has already been consumed and cannot be re-used.",
		code:         SyncNonceAlreadyConsumedCode,
	})
}

// PrimaryDomainAlreadyExists signifies an error when a new domain is added as
// primary when there is already once in the instance.
// Currently, we only support a single primary domain per instance.
func PrimaryDomainAlreadyExists() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "primary domain already exists",
		longMessage:  "Currently, only a single primary domain is supported and the current instance already has one. All new domains need to be set a satellites.",
		code:         PrimaryDomainAlreadyExistsCode,
		meta:         &formParameter{Name: "is_satellite"},
	})
}

func InvalidProxyConfiguration(msg string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: msg,
		longMessage:  "Clerk Frontend API cannot be accessed through the proxy URL. Make sure your proxy is configured correctly.",
		code:         InvalidProxyConfigurationCode,
		meta:         &formParameter{Name: "proxy_url"},
	})
}
