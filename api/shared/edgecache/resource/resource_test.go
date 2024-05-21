package resource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_bapiResource_hash(t *testing.T) {
	t.Parallel()
	resource := bapiResource{}

	// ensure that hash is computed after secret keys are normalized to always
	// contain the 'sk_' prefix
	functionallyEquivalentSecretKeys := []string{
		"sk_live_abc123",
		"live_abc123",
	}
	expectedHash := hashedInstanceSecretKey("e9982364fd73c3ea5cfbc3c032589e2b8d332cd405e37d6ed3277972c1743f11")

	for _, tc := range functionallyEquivalentSecretKeys {
		tc := tc
		t.Run(tc, func(t *testing.T) {
			t.Parallel()
			h, err := resource.hash(tc)
			require.NoError(t, err)
			assert.Equal(t, expectedHash, h)
		})
	}
}

func Test_bapiResource_hash_EmptySecret(t *testing.T) {
	t.Parallel()

	h, err := bapiResource{}.hash("")
	require.Error(t, err)
	assert.Empty(t, h)
}
