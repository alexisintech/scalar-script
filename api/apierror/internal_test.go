package apierror

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnexpectedErrorWithNilCause(t *testing.T) {
	t.Parallel()
	err := Unexpected(nil)
	assert.Contains(t, err.Error(), "There was an internal error on our servers. We've been notified and are working on fixing it.")
}
