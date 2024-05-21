package apierror

import (
	"fmt"
	"net/http"
	"strings"

	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
)

// InvalidRequestBody signifies an error when the body of the request does not conform to the expected format
func InvalidRequestBody(err error) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Request body invalid",
		longMessage:  "The request body is invalid. Please consult the API documentation for more information.",
		code:         RequestBodyInvalidCode,
		cause:        clerkerrors.Wrap(err, 1),
	})
}

// MissingQueryParameter denotes that the required query parameter, param, was
// not provided by the request.
func MissingQueryParameter(param string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: fmt.Sprintf("Missing query parameter '%s'", param),
		longMessage:  fmt.Sprintf("The query parameter '%s' is missing from the request. Please consult the API documentation for more information.", param),
		code:         MissingQueryParameterCode,
	})
}

func MissingOneOfQueryParameters(params ...string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Missing query parameter",
		longMessage:  fmt.Sprintf("Either of the following query parameters must be provided: %s.", strings.Join(params, ", ")),
		code:         MissingQueryParameterCode,
	})
}

func InvalidQueryParameterValue(param string, value string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: param + "is invalid",
		longMessage:  value + " does not match one of the allowed values for parameter " + param,
		code:         InvalidQueryParameterValueCode,
	})
}

func MalformedPublishableKey(key string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Malformed publishable key",
		longMessage:  fmt.Sprintf("Ensure the provided publishable key (%s) is the one displayed in Dashboard", key),
		code:         MalformedPublishableKeyCode,
	})
}

// OriginHeaderMissing
func OriginHeaderMissing() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Origin header missing",
		longMessage:  "This request requires an Origin header to be set, but it is missing",
		code:         OriginHeaderMissingCode,
	})
}

func InfiniteRedirectLoop() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Infinite redirect loop detected",
		longMessage:  "Infinite redirect loop detected. That usually means that we were not able to determine the auth state for this request.",
		code:         InfiniteRedirectLoopCode,
	})
}

// UnsupportedContentType signifies an error when provided content type is unsupported
func UnsupportedContentType(actual, expected string) Error {
	return New(http.StatusUnsupportedMediaType, &mainError{
		shortMessage: "Content-Type is unsupported",
		longMessage:  fmt.Sprintf("Content-Type %s is unsupported. You should use %s instead.", actual, expected),
		code:         UnsupportedContentTypeCode,
	})
}

// MalformedRequestParameters signifies an error when the request parameters are malformed and result in parsing errors
func MalformedRequestParameters(err error) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Malformed request parameters",
		longMessage:  "The request parameters are malformed and could not be parsed",
		code:         MalformedRequestParametersCode,
		cause:        clerkerrors.Wrap(err, 1),
	})
}

func ProxyRequestMissingSecretKey() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "missing secret key",
		longMessage:  "When using a proxy, it's required to also pass the instance secret key in the Clerk-Secret-Key header.",
		code:         ProxyRequestMissingSecretKeyCode,
	})
}

func ProxyRequestInvalidSecretKey() Error {
	return New(http.StatusUnauthorized, &mainError{
		shortMessage: "invalid secret key",
		longMessage:  "The secret key given with this proxy request is invalid.",
		code:         ProxyRequestInvalidSecretKeyCode,
	})
}

func BulkSizeExceeded() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "bulk size exceeded",
		longMessage:  fmt.Sprintf("Parameters exceed the maximum allowed bulk processing size of %d.", constants.MaxBulkSize),
		code:         BulkSizeExceededCode,
	})
}

func InvalidAPIVersion(reason string) Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "invalid API version",
		longMessage:  fmt.Sprintf("Invalid Clerk API version: %s", reason),
		code:         APIVersionInvalidCode,
	})
}
