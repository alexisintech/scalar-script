package apierror

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	clerkstrings "clerk/pkg/strings"

	"github.com/go-playground/validator/v10"
)

// FormDuplicateParameter signifies an error when a duplicate parameter is found in a form
func FormDuplicateParameter(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is duplicate",
		longMessage:  fmt.Sprintf("%s included multiple times. There should only be one.", param),
		code:         FormParamDuplicateCode,
		meta:         &formParameter{Name: param},
	})
}

// FormDuplicateParameterValue signifies an error when a value has been provided multiple times
func FormDuplicateParameterValue(param, value string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "duplicate values",
		longMessage:  fmt.Sprintf("%s contains duplicate values", value),
		code:         FormParamDuplicateCode,
		meta:         &formParameter{Name: param},
	})
}

// FormMaximumParametersExceeded signifies an error when more than 100 of the same param is included.
func FormMaximumParametersExceeded(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("%s is included more than the maximum of 100 times.", param),
		longMessage:  fmt.Sprintf("%s is included more than the maximum of 100 times.", param),
		code:         FormParamDuplicateCode,
		meta:         &formParameter{Name: param},
	})
}

// FormIdentifierExists signifies an error when given identifier already exists
func FormIdentifierExists(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("That %s is taken. Please try another.", clerkstrings.SnakeCaseToHumanReadableString(param)),
		code:         FormIdentifierExistsCode,
		meta:         &formParameter{Name: param},
	})
}

// FormIdentifierNotFound signifies an error when a required identifier is not found
func FormIdentifierNotFound(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Couldn't find your account.",
		code:         FormIdentifierNotFoundCode,
		meta:         &formParameter{Name: param},
	})
}

// FormAlreadyExists signifies an error when given resource already exists
func FormAlreadyExists(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("The %s already exists. Please try another.", clerkstrings.SnakeCaseToHumanReadableString(param)),
		code:         FormAlreadyExistsCode,
		meta:         &formParameter{Name: param},
	})
}

// FormIncorrectCode signifies an error when the given code is incorrect
func FormIncorrectCode(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is incorrect",
		longMessage:  "Incorrect code",
		code:         FormIncorrectCodeCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidParameterFormat signifies an error when the given parameter has an invalid format
func FormInvalidParameterFormat(param string, moreInfo ...string) Error {
	var msg strings.Builder
	msg.Write([]byte(fmt.Sprintf("%s is invalid.", clerkstrings.Capitalize(clerkstrings.SnakeCaseToHumanReadableString(param)))))
	for _, m := range moreInfo {
		msg.Write([]byte(" "))
		msg.Write([]byte(m))
	}
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: msg.String(),
		code:         FormParamFormatInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidParameterValue signifies an error when the given parameter has an invalid value
func FormInvalidParameterValue(param string, value string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  value + " does not match one of the allowed values for parameter " + param,
		code:         FormParamValueInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidParameterValueWithAllowed signifies an error when the given parameter has an invalid value.
// The difference with FormInvalidParameterValue is that this error also includes the allowed values
func FormInvalidParameterValueWithAllowed(param string, value string, allowed []string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage: fmt.Sprintf("%s does not match the allowed values for parameter %s. Allowed values: %s",
			value, param, clerkstrings.ToSentence(allowed, ", ", " or ")),
		code: FormParamValueInvalidCode,
		meta: &formParameter{Name: param},
	})
}

// FormInvalidEncodingParameterValue signifies an error when the given parameter has an invalid encoding
func FormInvalidEncodingParameterValue(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "invalid character encoding",
		longMessage:  param + " contains invalid UTF-8 characters",
		code:         FormParamValueInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

// FormDisabledParameterValue signifies an error when the given parameter has an invalid value because it is not enabled in the settings
func FormDisabledParameterValue(param string, value string) Error {
	return New(
		http.StatusBadRequest,
		&mainError{
			shortMessage: "is disabled",
			longMessage:  value + " is disabled. Please verify you're using the correct instance, or see our docs to learn how to enable this value.",
			code:         FormParamValueDisabled,
			meta:         &formParameter{Name: param},
		},
	)
}

// FormInvalidPasswordLengthTooShort signifies an error when the password is invalid because of its length
func FormInvalidPasswordLengthTooShort(param string, minLen int) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("Passwords must be %d characters or more.", minLen),
		code:         FormPasswordLengthTooShortCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidPasswordLengthTooLong signifies an error when the password is invalid because of its length
func FormInvalidPasswordLengthTooLong(param string, maxLen int) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("Passwords must be less than %d characters.", maxLen),
		code:         FormPasswordLengthTooLongCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidPasswordNoUppercase(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Passwords must contain at least one uppercase character.",
		code:         FormPasswordNoUppercaseCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidPasswordNoLowercase(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Passwords must contain at least one lowercase character.",
		code:         FormPasswordNoLowercaseCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidPasswordNoNumber(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Passwords must contain at least one number.",
		code:         FormPasswordNoNumberCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidPasswordNoSpecialChar(param, allowedSpecialChars string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("Passwords must contain at least one of the following special characters: %s.", allowedSpecialChars),
		code:         FormPasswordNoSpecialCharCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidPasswordNotStrongEnough(param string, suggestions ...ZXCVBNSuggestion) Error {
	meta := &passwordNotStrongEnoughParams{
		formParameter: formParameter{
			Name: param,
		},
		ZXCVBN: suggestionsParams{
			Suggestions: suggestions,
		},
	}
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Given password is not strong enough.",
		code:         FormPasswordNotStrongEnoughCode,
		meta:         meta,
	})
}

// FormInvalidPasswordSizeInBytesExceeded signifies that the size in bytes was exceeded.
// Note that the maximum character length constraint may fail to detect this case,
// if multi-byte characters are included in the password.
// For example, bcrypt limit https://cs.opensource.google/go/x/crypto/+/refs/tags/v0.8.0:bcrypt/bcrypt.go;l=87
func FormInvalidPasswordSizeInBytesExceeded(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Maximum size in bytes exceeded",
		code:         FormPasswordSizeInBytesExceededCode,
		meta:         &formParameter{Name: param},
	})
}

// FormPasswordValidationFailed signifies a generic error when the password validation failed
func FormPasswordValidationFailed(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Passwords validation failed. Try again.",
		code:         FormPasswordValidationFailedCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidTypeParameter signifies an error when a form parameter has the wrong type
func FormInvalidTypeParameter(param string, paramType string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  fmt.Sprintf("`%s` must be a `%s`.", param, paramType),
		code:         FormParamTypeInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

// FormMissingParameter signifies an error when an expected form parameter is missing
func FormMissingParameter(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is missing",
		longMessage:  fmt.Sprintf("%s must be included.", param),
		code:         FormParamMissingCode,
		meta:         &formParameter{Name: param},
	})
}

// FormMissingResource signifies an error when the form parameter is referring to a missing resource
func FormMissingResource(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is missing",
		longMessage:  fmt.Sprintf("The resource associated with the supplied %s was not found.", param),
		code:         FormResourceNotFoundCode,
		meta:         &formParameter{Name: param},
	})
}

// FormNilParameter signifies an error when a nil parameter is found in a form
func FormNilParameter(param string) Error {
	return FormNilParameterWithCustomText(param, clerkstrings.SnakeCaseToHumanReadableString(param))
}

// FormNilParameterWithCustomText signifies an error when a nil parameter is found in a form.
// This variant also accepts a custom text to be displayed.
func FormNilParameterWithCustomText(param, customText string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("Enter %s.", customText),
		code:         FormParamNilCode,
		meta:         &formParameter{Name: param},
	})
}

// FormMissingConditionalParameter signifies an error when required parameter based on conditions is missing
func FormMissingConditionalParameter(param string, leftCondition string, rightCondition string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is missing",
		longMessage:  fmt.Sprintf("`%s` is required when `%s` is `%s`.", param, leftCondition, rightCondition),
		code:         FormConditionalParamMissingCode,
		meta:         &formParameter{param},
	})
}

// FormAtLeastOneOptionalParameterMissing signifies an error when at least one optional parameter must be provided
func FormAtLeastOneOptionalParameterMissing(paramNames ...string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "at least one parameter must be provided",
		longMessage:  fmt.Sprintf("at least one of `%s` must be provided", strings.Join(paramNames, "`, `")),
		code:         FormParamMissingCode,
		meta:         &missingParameters{Names: paramNames},
	})
}

// FormInvalidInactivityTimeoutAgainstTimeToExpire signifies an error when the session inactivity timeout is greater than session time to expire
func FormInvalidInactivityTimeoutAgainstTimeToExpire(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  "Session inactivity timeout must be lower than maximum session lifetime.",
		code:         FormInvalidSessionInactivityTimeoutCode,
		meta:         &formParameter{Name: param},
	})
}

// FormMissingConditionalParameterOnExistence signifies an error when parameter is required because of the existence of another
func FormMissingConditionalParameterOnExistence(missingParam string, conditionalParam string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is missing",
		longMessage:  fmt.Sprintf("`%s` is required when `%s` is present.", missingParam, conditionalParam),
		code:         FormConditionalParamMissingCode,
		meta:         &formParameter{Name: missingParam},
	})
}

// FormParameterNotAllowedConditionally signifies an error when parameter is not allowed based on condition
func FormParameterNotAllowedConditionally(param string, leftCondition string, rightCondition string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is not allowed",
		longMessage:  fmt.Sprintf("`%s` isn't allowed when `%s` is %s.", param, leftCondition, rightCondition),
		code:         FormConditionalParamDisallowedCode,
		meta:         &formParameter{Name: param},
	})
}

// FormParameterValueNotAllowedConditionally signifies an error when parameter value is not allowed based on condition
func FormParameterValueNotAllowedConditionally(param, value, leftCondition, rightCondition string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("%s is not allowed", value),
		longMessage:  fmt.Sprintf("`%s` isn't allowed for `%s` when %s is %s.", value, param, leftCondition, rightCondition),
		code:         FormConditionalParamValueDisallowedCode,
		meta:         &formParameter{Name: param},
	})
}

// FormParameterNotAllowedIfAnotherParameterIsPresent signifies an error when a parameter is present but
// is not allowed because another parameter is also present
func FormParameterNotAllowedIfAnotherParameterIsPresent(notAllowedParam string, existingParam string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is not allowed",
		longMessage:  fmt.Sprintf("`%s` isn't allowed when `%s` is present.", notAllowedParam, existingParam),
		code:         FormConditionalParamDisallowedCode,
		meta:         &formParameter{Name: notAllowedParam},
	})
}

// FormPasswordIncorrect signifies an error when given password is incorrect
func FormPasswordIncorrect(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Password is incorrect. Try again, or use another method.",
		code:         FormPasswordIncorrectCode,
		meta:         &formParameter{Name: param},
	})
}

// FormPwnedPassword signifies an error when the chosen password has been found in the pwned list
func FormPwnedPassword(param string, isSignIn bool) Error {
	action := "use a different password"

	if isSignIn {
		action = "reset your password"
	}

	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Password has been found in an online data breach. For account safety, please " + action + ".",
		code:         FormPasswordPwnedCode,
		meta:         &formParameter{Name: param},
	})
}

// FormPasswordDigestInvalid signifies an error when the provided password_digest is not valid for the provided password_hasher
func FormPasswordDigestInvalid(param string, hasher string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("The provided %s is not a valid %s password hash.", param, hasher),
		code:         FormPasswordDigestInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

// FormValidationFailed converts validator.ValidationErrors to Error.
func FormValidationFailed(err error) Error {
	var validationErrors validator.ValidationErrors
	if !errors.As(err, &validationErrors) {
		return Unexpected(err)
	}

	var apiErrs Error
	for _, validationErr := range validationErrors {
		switch {
		case validationErr.Tag() == "required" || validationErr.Tag() == "required_if":
			sanitizedField := clerkstrings.ToSnakeCase(validationErr.Field())
			apiErrs = Combine(apiErrs, FormMissingParameter(sanitizedField))
		case validationErr.Tag() == "max" && validationErr.Kind() == reflect.String:
			sanitizedField := clerkstrings.ToSnakeCase(validationErr.Field())
			maxLength, _ := strconv.Atoi(validationErr.Param())
			apiErrs = Combine(apiErrs, FormParameterMaxLengthExceeded(sanitizedField, maxLength))
		default:
			sanitizedField := clerkstrings.ToSnakeCase(validationErr.Field())
			apiErrs = Combine(apiErrs, New(http.StatusUnprocessableEntity, &mainError{
				shortMessage: "is invalid",
				longMessage:  sanitizedField + " is invalid",
				code:         FormParamValueInvalidCode,
				meta:         &formParameter{Name: sanitizedField},
			}))
		}
	}
	return apiErrs
}

// FormInvalidOrigin signifies an error when the given origin is http/https
func FormInvalidOrigin(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  fmt.Sprintf("%s must be a valid origin such as my-app://localhost, chrome-extension://mnhbilbfebpbokpjjamapdecdgieldho, or capacitor://localhost:3000", param),
		code:         FormInvalidOriginCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidTime(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "invalid format",
		longMessage:  fmt.Sprintf("%s must contain a datetime specified in RFC3339 format (e.g. `2022-10-20T10:00:27.645Z`).", param),
		code:         FormInvalidTimeCode,
		meta:         &formParameter{Name: param},
	})
}

// FormUnknownParameter signifies an error when an unexpected parameter is found in a form
func FormUnknownParameter(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is unknown",
		longMessage:  fmt.Sprintf("%s is not a valid parameter for this request.", param),
		code:         FormParamUnknownCode,
		meta:         &formParameter{Name: param},
	})
}

// FormUnverifiedIdentification signifies an error when the identification included in the form is unverified
func FormUnverifiedIdentification(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is unverified",
		longMessage:  "This identification needs to be verified before you can perform this action.",
		code:         FormIdentificationNeededCode,
		meta:         &formParameter{Name: param},
	})
}

// FormParameterSizeTooLarge signifies an error when a parameter exceeds the max allowed size
func FormParameterSizeTooLarge(param string, maxByteSize int) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("The given %s exceeds the maximum allowed size of %d bytes (%d KB).", param, maxByteSize, maxByteSize/1024),
		code:         FormParamExceedsAllowedSizeCode,
		meta:         &formParameter{Name: param},
	})
}

func FormParameterValueTooLarge(param string, max int) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Value too large",
		longMessage:  fmt.Sprintf("The value of %s can't be greater than %d", param, max),
		code:         FormParameterValueTooLargeCode,
		meta:         &formParameter{Name: param},
	})
}

// FormMetadataInvalidType signifies an error when the given metadata is not a valid key-value object
func FormMetadataInvalidType(param string) Error {
	metadataType := clerkstrings.SnakeCaseToHumanReadableString(param)
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf(`%s must be a valid key-value object. To reset the %s, use an empty object ("{}").`, clerkstrings.Capitalize(metadataType), metadataType),
		code:         FormParamValueInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidEmailAddress(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  fmt.Sprintf("%s must be a valid email address.", param),
		code:         FormParamFormatInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidEmailAddresses(invalidEmailAddresses []string, param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "invalid email addresses",
		longMessage:  fmt.Sprintf("The following email addresses are invalid: %s", strings.Join(invalidEmailAddresses, ", ")),
		code:         FormParamFormatInvalidCode,
		meta: &formInvalidEmailAddresses{
			formParameter: formParameter{
				Name: param,
			},
			EmailAddresses: invalidEmailAddresses,
		},
	})
}

func FormInvalidEmailLocalPart(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  fmt.Sprintf("%s must be a valid email address local part.", param),
		code:         FormParamFormatInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

func FormEmailAddressBlocked(param string) Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "disposable email address not allowed",
		longMessage:  "Disposable email addresses are not allowed. Please choose a permanent one or contact support.",
		code:         FormEmailAddressBlockedCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidPhoneNumber(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  fmt.Sprintf("%s must be a valid phone number according to E.164 international standard.", param),
		code:         FormParamFormatInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidIdentifier(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  fmt.Sprintf("%s must be either a valid email address, a valid phone number according to E.164 international standard or a valid web3 wallet.", param),
		code:         FormParamFormatInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidWeb3Wallet signifies an error when the given web3 wallet address is invalid
func FormInvalidWeb3WalletAddress(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is invalid",
		longMessage:  fmt.Sprintf("%s must be a valid web3 wallet address that starts with 0x and contains 40 hexadecimal characters.", param),
		code:         FormParamFormatInvalidCode,
		meta:         &formParameter{Name: param},
	})
}

func FormIncorrectSignature(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "is incorrect",
		longMessage:  "Incorrect signature",
		code:         FormIncorrectSignatureCode,
		meta:         &formParameter{Name: param},
	})
}

// FormParameterMaxLengthExceeded signifies an error when the given param value exceeds the maximum allowed length
func FormParameterMaxLengthExceeded(param string, max int) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "exceeds maximum length",
		longMessage:  fmt.Sprintf("%s should not exceed %d characters.", clerkstrings.Capitalize(clerkstrings.SnakeCaseToHumanReadableString(param)), max),
		code:         FormParameterMaxLengthExceededCode,
		meta:         &formParameter{Name: param},
	})
}

// FormParameterMinLengthExceeded signifies an error when the given param value is less than the minimum allowed length
func FormParameterMinLengthExceeded(param string, min int) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "does not reach minimum length",
		longMessage:  fmt.Sprintf("%s must be at least %d characters long.", clerkstrings.Capitalize(clerkstrings.SnakeCaseToHumanReadableString(param)), min),
		code:         FormParameterMinLengthExceededCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidUsernameLength signifies an error when the given username does not have required length
func FormInvalidUsernameLength(param string, min, max int) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("%s must be between %d and %d characters long.", clerkstrings.Capitalize(clerkstrings.SnakeCaseToHumanReadableString(param)), min, max),
		code:         FormUsernameInvalidLengthCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidUsernameNeedsNonNumberCharCode signifies an error when the given username does not match username regex
func FormInvalidUsernameNeedsNonNumberCharCode(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("%s must contain one non-number character.", clerkstrings.Capitalize(clerkstrings.SnakeCaseToHumanReadableString(param))),
		code:         FormUsernameNeedsNonNumberCharCode,
		meta:         &formParameter{Name: param},
	})
}

// FormInvalidUsernameCharacter signifies an error when the given username does not match username regex
func FormInvalidUsernameCharacter(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("%s can only contain letters, numbers and '_' or '-'.", clerkstrings.Capitalize(clerkstrings.SnakeCaseToHumanReadableString(param))),
		code:         FormUsernameInvalidCharacterCode,
		meta:         &formParameter{Name: param},
	})
}

// FormNotAllowedToDisableDefaultSecondFactor signifies an error when trying to disable the default flag from a second-factor
func FormNotAllowedToDisableDefaultSecondFactor(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "The default second factor method can only be changed by assigning another method as the default.",
		code:         FormNotAllowedToDisableDefaultSecondFactorCode,
		meta:         &formParameter{Name: param},
	})
}

func FormInvalidDate(param string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Date values must be given in Unix millisecond timestamp format with day precision.",
		code:         FormInvalidDateCode,
		meta:         &formParameter{Name: param},
	})
}
