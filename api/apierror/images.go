package apierror

import (
	"fmt"
	"net/http"
)

// RequestWithoutImage signifies an error when no image was present in the request.
func RequestWithoutImage() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Image file missing",
		longMessage:  "There was no image file present in the request",
		code:         FormParamMissingCode,
	})
}

// ImageTooLarge signifies an error when the image being uploaded is too large to handle.
func ImageTooLarge() Error {
	return New(http.StatusRequestEntityTooLarge, &mainError{
		shortMessage: "Image too large",
		longMessage:  "The image being uploaded is more than 10MB. Please choose a smaller one.",
		code:         ImageTooLargeCode,
	})
}

func ImageNotFound() Error {
	return New(http.StatusNotFound, &mainError{
		shortMessage: "Image not found",
		code:         ImageNotFoundCode,
	})
}

func ImageTypeNotSupported(imageType string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "unsupported image type",
		longMessage: fmt.Sprintf("'%s' images are not currently supported. Please consult the API documentation for more information.",
			imageType),
		code: RequestBodyInvalidCode,
	})
}
