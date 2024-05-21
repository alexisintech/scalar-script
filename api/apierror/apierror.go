// package apierror defines the main error type (Error) used throughout our
// APIs. It also defines actual error instances.
package apierror

import (
	"bytes"
	stderr "errors"
	"fmt"
	"net/http"
	"reflect"

	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/strings"
)

// Error is an error that will be returned by our APIs. It may optionally
// wrap one or more underlying errors.
type Error interface {
	error

	Errors() []APIError
	HTTPCode() int
	Meta() interface{}
	WithMeta(interface{}) Error
	IsTypeOf(string) bool
	ErrorCode() string
	ToErrorResponses() []ErrorResponse
}

// APIError represents a single error
type APIError interface {
	error
	ShortMessage() string
	LongMessage() string
	Code() string
	Meta() interface{}
	Cause() error
}

type mainErrors struct {
	errors   []APIError
	httpCode int
	meta     interface{}
}

// New creates an APIErrors with a single Error
func New(httpCode int, e APIError) Error {
	return &mainErrors{
		errors:   []APIError{e},
		httpCode: httpCode,
	}
}

type mainError struct {
	shortMessage string
	longMessage  string
	code         string
	meta         interface{}
	cause        error
}

// IsInternal checks whether the given APIErrors is an internal error or not
func IsInternal(err Error) bool {
	if err == nil {
		return false
	}
	return err.HTTPCode() >= http.StatusInternalServerError
}

// As wraps the `errors.As` to make it easier to check whether an error is in fact an API error.
// The reasons we included it versus accessing the `errors.As` directly are:
// 1. We don't have access to `mainErrors` outside this package
// 2. It's easier to include in longer if-else chain of statements
func As(err error) (Error, bool) {
	var apiErr *mainErrors
	if isAPIErr := stderr.As(err, &apiErr); isAPIErr {
		return apiErr, true
	}
	return nil, false
}

// Combine creates a new instance of APIErrors by combining the two given ones
// If the HTTP codes do not match, the HTTP code is decided based on ....
func Combine(err1, err2 Error) Error {
	if err1 == nil {
		return err2
	} else if err2 == nil {
		return err1
	}

	newHTTPCode := determineHTTPCode(err1.HTTPCode(), err2.HTTPCode())
	newErrors := append(err1.Errors(), err2.Errors()...)

	return &mainErrors{
		errors:   newErrors,
		httpCode: newHTTPCode,
		meta:     err1.Meta(),
	}
}

func determineHTTPCode(a int, b int) int {
	if a == b {
		return a
	}
	max := findMax(a, b)
	return (max / 100) * 100
}

func findMax(a, b int) int {
	if a >= b {
		return a
	}
	return b
}

// CauseMatches will run the matcher callback for every error Cause and
// return true if at least one match is found.
// The matcher callback is not invoked for nil cause errors.
func CauseMatches(apiErr Error, matcher func(cause error) bool) bool {
	for _, e := range apiErr.Errors() {
		if e.Cause() == nil {
			continue
		}
		if matcher(e.Cause()) {
			return true
		}
	}
	return false
}

// IsTypeOf checks whether the given error is of a certain type
func (m *mainErrors) IsTypeOf(errType string) bool {
	for _, err := range m.errors {
		if err.Code() == errType {
			return true
		}
	}
	return false
}

func (m *mainErrors) Error() string {
	var b bytes.Buffer
	b.WriteString(fmt.Sprintf("%d status code\n", m.httpCode))
	b.WriteString("Errors [\n")
	for _, err := range m.errors {
		b.WriteString(err.Error() + "\n")
	}
	b.WriteString("]\n")
	return b.String()
}

func (m *mainErrors) ErrorCode() string {
	if len(m.errors) == 0 {
		return ""
	}
	return m.errors[0].Code()
}

// Errors returns all enclosed errors
func (m *mainErrors) Errors() []APIError {
	return m.errors
}

func (m *mainErrors) HTTPCode() int {
	return m.httpCode
}

func (m *mainErrors) Meta() interface{} {
	return m.meta
}

func (m *mainErrors) WithMeta(meta interface{}) Error {
	m.meta = meta
	return m
}

func (m *mainErrors) ToErrorResponses() []ErrorResponse {
	errorResponses := make([]ErrorResponse, len(m.Errors()))
	for i, err := range m.Errors() {
		errorResponses[i] = ErrorResponse{
			ShortMessage: err.ShortMessage(),
			LongMessage:  err.LongMessage(),
			Code:         err.Code(),
			Meta:         err.Meta(),
		}

		if cenv.IsEnabled(cenv.ClerkDebugMode) {
			if err.Cause() != nil {
				errorResponses[i].Cause = strings.ToPrintable(err.Error())
			}
		}
	}
	return errorResponses
}

func (m *mainError) String() string {
	return fmt.Sprintf("msg=%s, long_msg=%s, code=%s", m.shortMessage, m.longMessage, m.code)
}

// For our own errors, we have the following logic regarding the Error() method
// * If there is no particular cause for the error (i.e. it was something we generated), we use the long message
// * If there is an underlying error that caused it, we try to get the stack trace of the error
// * If there is an underlying error but no stack trace, we settle for the long message and the message of the underlined error
func (m *mainError) Error() string {
	if m.cause == nil || reflect.ValueOf(m.cause).IsNil() {
		return m.longMessage
	}

	if stackTrace, containsStackTrace := clerkerrors.GetStackTrace(m.cause); containsStackTrace {
		return stackTrace
	}

	return fmt.Sprintf("%s\nCaused by: %s", m.longMessage, m.cause.Error())
}

func (m *mainError) ShortMessage() string {
	return m.shortMessage
}

func (m *mainError) LongMessage() string {
	if m.longMessage == "" {
		return m.shortMessage
	}
	return m.longMessage
}

func (m *mainError) Code() string {
	return m.code
}

func (m *mainError) Meta() interface{} {
	return m.meta
}

func (m *mainError) Cause() error {
	return m.cause
}
