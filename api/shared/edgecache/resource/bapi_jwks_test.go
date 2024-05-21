package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBapiJWKS_CacheTag(t *testing.T) {
	t.Parallel()

	expectedTag := "a9cba123e982b2a1fe3f3d2f9ed2c1ae4846c5224cced2c567f4502737e79ca0:/v1/jwks"

	r, err := NewBapiJWKS("sk_test_d34db33f")
	require.NoError(t, err)
	assert.Equal(t, expectedTag, r.CacheTag())
}
