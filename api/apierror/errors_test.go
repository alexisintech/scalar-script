package apierror

import (
	"errors"
	"testing"

	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"

	"github.com/stretchr/testify/assert"
)

func TestMainError_Error_noCause(t *testing.T) {
	t.Parallel()
	mainErr := mainError{
		shortMessage: "short",
		longMessage:  "long",
		code:         "code",
	}
	actual := mainErr.Error()
	assert.Equal(t, mainErr.longMessage, actual)
}

func TestMainError_Error_withCauseNoStackTrace(t *testing.T) {
	t.Parallel()
	mainErr := mainError{
		shortMessage: "short",
		longMessage:  "long",
		code:         "code",
		cause:        errors.New("cause"),
	}
	expected := "long\nCaused by: cause"
	actual := mainErr.Error()
	assert.Equal(t, expected, actual)
}

func TestMainError_Error_withCauseAndStackTrace(t *testing.T) {
	t.Parallel()

	mainErr := mainError{
		shortMessage: "short",
		longMessage:  "long",
		code:         "code",
		cause:        clerkerrors.Wrap(errors.New("cause"), 0),
	}
	actual := mainErr.Error()
	assert.Contains(t, actual, "errors_test.go")
}

func TestCauseMatches(t *testing.T) {
	t.Parallel()

	type args struct {
		apiErr  Error
		matcher func(cause error) bool
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "if cause is nil, the matcher is not called",
			args: args{
				apiErr: IdentificationExists(constants.ITUsername, nil),
				matcher: func(cause error) bool {
					return true
				},
			},
			want: false,
		},
		{
			name: "cause is not nil and matcher returns true",
			args: args{
				apiErr: IdentificationExists(constants.ITUsername, clerkerrors.ErrIdentificationClaimed),
				matcher: func(cause error) bool {
					return errors.Is(cause, clerkerrors.ErrIdentificationClaimed)
				},
			},
			want: true,
		},
		{
			name: "cause is not nil and matcher returns false",
			args: args{
				apiErr: IdentificationExists(constants.ITUsername, clerkerrors.ErrIdentificationClaimed),
				matcher: func(cause error) bool {
					return false
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equalf(t, tt.want, CauseMatches(tt.args.apiErr, tt.args.matcher), "CauseMatches(%v, %v)", tt.args.apiErr, tt.args.matcher)
		})
	}
}
