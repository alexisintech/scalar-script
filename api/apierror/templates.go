package apierror

import (
	"fmt"
	"net/http"

	"clerk/pkg/constants"
)

// TemplateTypeUnsupported signifies an error when an invalid template type is provided
func TemplateTypeUnsupported(templateType string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Template type not supported",
		longMessage:  "Template type " + templateType + " is not supported",
		code:         TemplateTypeUnsupportedCode,
	})
}

// TemplateNotFound signifies an error when no template with given slug was found
func TemplateNotFound(slug string) Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Template not found",
		longMessage:  "No template was found with slug " + slug,
		code:         ResourceNotFoundCode,
	})
}

// TemplateDeletionRestricted signifies an error when a deletion is attempted for a built-in (non-custom) template
func TemplateDeletionRestricted(slug string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Template deletion restricted",
		longMessage:  "Template with slug " + slug + " can't be deleted",
		code:         TemplateDeletionRestrictedCode,
	})
}

// TemplateRevertRestricted signifies an error when a custom template is attempted to be reverted
func TemplateRevertRestricted(slug string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Template revert restricted",
		longMessage:  "Template with slug " + slug + " can't be reverted",
		code:         TemplateRevertRestrictedCode,
	})
}

func CustomTemplateRequired(slug string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Custom template required",
		longMessage:  "Only custom templates can be used for this operation - " + slug + " is a built-in template",
		code:         CustomTemplateRequiredCode,
	})
}

func CustomTemplatesNotAvailable() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Custom templates not available",
		longMessage:  "Custom templates are not available, you can only use built-in templates",
		code:         CustomTemplatesNotAvailableCode,
	})
}

func RequiredVariableMissing(requiredVariable string) Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: fmt.Sprintf("should contain {{%s}} variable", requiredVariable),
		longMessage:  fmt.Sprintf("Body should contain the {{%s}} variable", requiredVariable),
		code:         RequiredVariableMissingCode,
		meta:         &formParameter{Name: "body"},
	})
}

func InvalidTemplateBody() Error {
	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Invalid template body",
		longMessage:  "This template body is invalid and cannot be rendered successfully, please check for syntax errors",
		code:         InvalidTemplateBodyCode,
		meta:         &formParameter{Name: "body"},
	})
}

func SMSMaxLengthExceeded(encoding string) Error {
	longMessage := ""

	if encoding == constants.SMSEncodingGSM7 {
		longMessage = "Messages based on this template may exceed 160 characters, please shorten the message to avoid extraneous charges."
	} else {
		longMessage = "Messages based on this template may exceed 140 unicode characters, please shorten the message to avoid extraneous charges."
	}

	return New(http.StatusUnprocessableEntity, &mainError{
		shortMessage: "Message length exceeded",
		longMessage:  longMessage,
		code:         SMSTemplateMaxLengthExceededCode,
		meta:         &formParameter{Name: "body"},
	})
}
