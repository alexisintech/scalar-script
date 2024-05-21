package apierror

import (
	"context"

	"clerk/pkg/cenv"
	"clerk/pkg/ctx/trace"
	"clerk/pkg/strings"
)

// Response is the JSON representation of a collection of errors
type Response struct {
	Errors       []ErrorResponse `json:"errors"`
	Meta         interface{}     `json:"meta,omitempty"`
	Cause        interface{}     `json:"cause,omitempty"`
	ClerkTraceID string          `json:"clerk_trace_id,omitempty"`
}

// ErrorResponse is the JSON representation of a single error
type ErrorResponse struct {
	ShortMessage string      `json:"message"`
	LongMessage  string      `json:"long_message"`
	Code         string      `json:"code"`
	Meta         interface{} `json:"meta,omitempty"`
	Cause        []string    `json:"cause,omitempty"`
}

// ToResponse converts an APIError to a Response
func ToResponse(ctx context.Context, e Error) Response {
	errorResponses := make([]ErrorResponse, len(e.Errors()))
	for i, err := range e.Errors() {
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
	return Response{
		Errors:       errorResponses,
		Meta:         e.Meta(),
		ClerkTraceID: trace.FromContext(ctx),
	}
}
