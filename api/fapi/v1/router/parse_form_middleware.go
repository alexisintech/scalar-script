package router

import (
	"net/http"
	"strings"

	"clerk/api/apierror"
	"clerk/api/shared/character_validator"
)

// parseForm middleware sets up `r.Form` to do param checking in requests.
func parseForm(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if err := r.ParseForm(); err != nil {
		return nil, apierror.MalformedRequestParameters(err)
	}

	// remove `[]` suffix from keys that have it
	// to account for the query string param syntax: ?test[]=blah&test[]=blah
	for key, val := range r.Form {
		if strings.HasSuffix(key, "[]") {
			newKey := strings.TrimSuffix(key, "[]")
			r.Form[newKey] = append(r.Form[newKey], val...)
			delete(r.Form, key)
		}
	}

	return r, nil
}

// validateCharSet checks the params for invalid characters
func validateCharSet(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if err := character_validator.Path(r); err != nil {
		return r, err
	}
	if err := character_validator.Form(r); err != nil {
		return r, err
	}
	if err := character_validator.QueryParams(r); err != nil {
		return r, err
	}
	return r, nil
}
