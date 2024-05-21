package apierror

import (
	"clerk/pkg/constants"
	"fmt"
	"net/http"
)

// UserNotFound signifies an error when no user is found with userID
func UserNotFound(userID string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "not found",
		longMessage:  "No user was found with id " + userID,
		code:         ResourceNotFoundCode,
	})
}

// DeleteLinkedCommNotAllowed signifies an error when trying to delete a linked communication
// TODO Alex: Check if this is the correct place for this error
func DeleteLinkedCommNotAllowed() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Deleting a linked email address is not allowed",
		longMessage:  "This email address is linked to one or more Connected Accounts. Remove the Connected Account before deleting this email address.",
		code:         DeleteLinkedIdentificationDisallowedCode,
	})
}

func UserDataMissing(missingParams []string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "missing data",
		longMessage:  "Supplied data doesn't match user requirements set for this instance",
		code:         FormDataMissing,
		meta:         &missingParameters{Names: missingParams},
	})
}

func PasswordRequired() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "password required",
		longMessage:  "Settings for this instance require a password to be set. Cannot remove the user's password.",
		code:         PasswordRequiredCode,
	})
}

func NoPasswordSet() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "no password set",
		longMessage:  "This user does not have a password set for their account",
		code:         NoPasswordSetCode,
	})
}

func IncorrectPassword() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "incorrect password",
		longMessage:  "The provided password is not the one the user has set",
		code:         IncorrectPasswordCode,
	})
}

func TOTPDisabled() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "TOTP is disabled",
		longMessage:  "This user does not have TOTP enabled in their account",
		code:         TOTPDisabledCode,
	})
}

func IncorrectTOTP() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "incorrect TOTP",
		longMessage:  "The provided TOTP code is incorrect",
		code:         IncorrectTOTPCode,
	})
}

func InvalidLengthTOTP() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "invalid length",
		longMessage:  "The provided TOTP code must be 6 characters long.",
		code:         InvalidLengthTOTPCode,
	})
}

func UserQuotaExceeded(maxAllowed int, instanceEnvironmentType string) Error {
	longMessage := ""

	switch constants.ToEnvironmentType(instanceEnvironmentType) {
	case constants.ETProduction:
		longMessage = fmt.Sprintf("You have reached your limit of %d users. You can remove the user limit by upgrading to a paid plan.", maxAllowed)
	case constants.ETDevelopment, constants.ETStaging:
		longMessage = fmt.Sprintf("You have reached your limit of %d users. If you need more users, please use a Production instance.", maxAllowed)
	}
	return New(http.StatusForbidden, &mainError{
		shortMessage: "user quota exceeded",
		longMessage:  longMessage,
		code:         UserQuotaExceededCode,
	})
}

func UpdatingUserPasswordDeprecated() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "deprecated feature",
		longMessage:  "Password is not a valid parameter and can only be updated via /v1/me/change_password",
		code:         UpdatingUserPasswordDeprecatedCode,
	})
}

func UserDeleteSelfNotEnabled() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "delete self not enabled",
		longMessage:  "Self deletion is not enabled for this user",
		code:         UserDeleteSelfNotEnabledCode,
	})
}

func UserCreateOrgNotEnabled() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "create organization not enabled",
		longMessage:  "Organization creation is not enabled for this user",
		code:         UserCreateOrganizationNotEnabledCode,
	})
}
