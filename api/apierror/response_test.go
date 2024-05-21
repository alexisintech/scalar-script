package apierror

import (
	"context"
	"errors"
	"testing"

	"clerk/pkg/cenv"
	"clerk/pkg/ctx/trace"

	"github.com/stretchr/testify/assert"
)

// nolint:paralleltest
func TestToResponse_includesCauseIfDebugMode(t *testing.T) {
	err := Unexpected(errors.New("test error"))
	t.Setenv(cenv.ClerkDebugMode, "true")

	response := ToResponse(context.Background(), err)
	assert.Len(t, response.Errors, 1)
	assert.NotEmpty(t, response.Errors[0].Cause)
}

// nolint:paralleltest
func TestToResponse_notIncludesCauseIfNotDebugMode(t *testing.T) {
	err := Unexpected(errors.New("test error"))
	t.Setenv(cenv.ClerkDebugMode, "false")

	response := ToResponse(context.Background(), err)
	assert.Len(t, response.Errors, 1)
	assert.Empty(t, response.Errors[0].Cause)
}

func TestToResponse_includesTraceIDIfPresent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	err := Unexpected(errors.New("test error"))

	response := ToResponse(ctx, err)
	assert.Empty(t, response.ClerkTraceID)

	ctx = trace.NewContext(ctx, "dummy-trace")
	response = ToResponse(ctx, err)
	assert.Equal(t, "dummy-trace", response.ClerkTraceID)
}
