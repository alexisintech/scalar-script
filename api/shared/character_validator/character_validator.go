package character_validator

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"clerk/api/apierror"
)

func Path(r *http.Request) apierror.Error {
	if containsInvalidCharacters(r.URL.Path) {
		return apierror.ResourceNotFound()
	}
	return nil
}

func Form(r *http.Request) apierror.Error {
	var encodingErrors apierror.Error

	for param, val := range r.Form {
		for _, stringVal := range val {
			err := validateCharacterEncoding(param, stringVal)
			encodingErrors = apierror.Combine(encodingErrors, err)
		}
	}

	return encodingErrors
}

func QueryParams(r *http.Request) apierror.Error {
	var encodingErrors apierror.Error

	for paramName, paramValues := range r.URL.Query() {
		for _, value := range paramValues {
			err := validateCharacterEncoding(paramName, value)
			encodingErrors = apierror.Combine(encodingErrors, err)
		}
	}

	return encodingErrors
}

var disallowedRunes = []rune{
	0x00, // NUL - cannot be persisted by Postgres
}

func validateCharacterEncoding(param, value string) apierror.Error {
	if containsInvalidCharacters(value) {
		return apierror.FormInvalidEncodingParameterValue(param)
	}
	return nil
}

func containsInvalidCharacters(value string) bool {
	if !utf8.ValidString(value) {
		return true
	}

	for _, disallowedRune := range disallowedRunes {
		if strings.ContainsRune(value, disallowedRune) {
			return true
		}
	}
	return false
}
