package apierror

import (
	"fmt"
	"net/http"
)

// ReservedDomain signifies an error when the domain extracted from the provided home_url is reserved by Clerk
func ReservedDomain(domain string, paramName string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Domain reserved by Clerk",
		longMessage:  fmt.Sprintf("The %s domain is reserved by Clerk.", domain),
		code:         ReservedDomainCode,
		meta:         &formParameter{Name: paramName},
	})
}

// KnownHostingDomain signifies an error when the domain extracted from the provided home_url belongs to a
// known hosting service and cannot be used to deploy production apps
func KnownHostingDomain(domain string, paramName string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Known hosting domain",
		longMessage:  fmt.Sprintf("The %s domain cannot be used to deploy production apps.", domain),
		code:         KnownHostingDomainCode,
		meta:         &formParameter{Name: paramName},
	})
}

// ReservedSubdomain signifies an error when the subdomain extracted from the provided home_url is reserved by Clerk
func ReservedSubdomain(subdomain string, paramName string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Reserved subdomain",
		longMessage:  fmt.Sprintf("The %s subdomain is reserved by Clerk.", subdomain),
		code:         ReservedSubdomainCode,
		meta:         &formParameter{Name: paramName},
	})
}

// HomeURLTaken signifies an error when the root domain of the provided home_url already in use by another application
func HomeURLTaken(homeURL string, paramName string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Domain already in use",
		longMessage:  fmt.Sprintf("The %s root domain is already in use by another application.", homeURL),
		code:         HomeURLTakenCode,
		meta:         &formParameter{Name: paramName},
	})
}
