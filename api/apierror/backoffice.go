package apierror

import (
	"fmt"
	"net/http"
)

func CannotSetUnlimitedSeatsForUserApplication() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "not allowed to set unlimited seats",
		longMessage:  "Cannot set unlimited seats for user applications.",
		code:         CannotSetUnlimitedSeatsForUserApplicationCode,
	})
}

func CannotUnsetUnlimitedSeatsForOrganization() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "not allowed to unset unlimited seats",
		longMessage:  "Cannot unset unlimited seats for organizations.",
		code:         CannotUnsetUnlimitedSeatsForOrganizationCode,
	})
}

func CannotUpdateUserLimitsOnProductionInstance() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Cannot update user limits on production instances.",
		code:         CannotUpdateUserLimitsOnProductionCode,
	})
}

func CannotUpdateGivenDomain(domain string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "not allowed to update domain",
		longMessage:  fmt.Sprintf("Domain %s cannot be updated.", domain),
		code:         CannotUpdateGivenDomainCode,
	})
}

func EmailDomainNotFound(domain string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "email domain not found",
		longMessage:  fmt.Sprintf("Email domain %s wasn't found.", domain),
		code:         EmailDomainNotFoundCode,
	})
}
