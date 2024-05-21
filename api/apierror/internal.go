package apierror

import (
	"net/http"

	"clerk/pkg/clerkerrors"
)

// Unexpected is used for all unexpected errors
func Unexpected(err error) Error {
	return New(http.StatusInternalServerError, &mainError{
		shortMessage: "Oops, an unexpected error occurred",
		longMessage:  "There was an internal error on our servers. We've been notified and are working on fixing it.",
		code:         InternalClerkErrorCode,
		cause:        clerkerrors.Wrap(err, 1),
	})
}

func TooManyRequests() Error {
	return New(http.StatusTooManyRequests, &mainError{
		shortMessage: "Too many requests",
		longMessage:  "Too many requests, retry later",
		code:         TooManyRequestsCode,
	})
}

// 403 - quota exceeded
func QuotaExceeded() Error {
	return New(http.StatusForbidden, &mainError{
		shortMessage: "Quota exceeded",
		longMessage:  "Quota exceeded, you have reached your limit.",
		code:         QuotaExceededCode,
	})
}

// 409 - conflict
func Conflict() Error {
	return New(http.StatusConflict, &mainError{
		shortMessage: "Conflict",
		longMessage:  "Conflict",
		code:         ConflictCode,
	})
}

func BadRequest() Error {
	return New(http.StatusBadRequest, &mainError{
		shortMessage: "Bad request",
		longMessage:  "Bad request",
		code:         BadRequestCode,
	})
}

func CannotDetectIP(msg string) Error {
	return New(http.StatusServiceUnavailable, &mainError{
		shortMessage: msg,
		longMessage:  msg,
		code:         CannotDetectIPCode,
	})
}
