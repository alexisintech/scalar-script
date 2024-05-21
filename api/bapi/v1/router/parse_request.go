package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/character_validator"
)

// validateCharSet checks the params for invalid characters
func validateCharSet(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if err := character_validator.Path(r); err != nil {
		return r, err
	}
	if err := character_validator.QueryParams(r); err != nil {
		return r, err
	}

	return r, nil
}
