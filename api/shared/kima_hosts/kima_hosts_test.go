package kima_hosts

import (
	"testing"

	"clerk/pkg/cenv"

	"github.com/stretchr/testify/assert"
)

func TestSharedDevDomain(t *testing.T) {
	t.Parallel()

	assert.NotEmpty(t, cenv.Get(cenv.ClerkPublishableKeySharedDevDomain))

	testcases := map[string]bool{
		"https://happy-hippo-1.clerk.accounts.lclclerk.com":          true,
		"https://happy-hippo-1.accounts.lclclerk.com":                true,
		"https://happy-hippo-1.clerk.accounts.lclclerk.com/hi":       true,
		"https://happy-hippo-1.accounts.lclclerk.com/foo/bar?hi=1#2": true,
		"happy-hippo-1.clerk.accounts.lclclerk.com":                  true,
		"happy-hippo-1.accounts.lclclerk.com":                        true,
		"happy-hippo-1.clerk.accounts.lclclerk.com/hi":               false,
		"happy-hippo-1.accounts.lclclerk.com/foo":                    false,
		"https://happy-hippo-1.clerk.lclclerk.com ":                  false,
		"happy-hippo-1.lclclerk.com":                                 false,
		"happy-hippo-1.clerk.lclclerk.com":                           false,
		"clerk.happy.hippo-1.dev.lclclerk.com":                       false,
		"accounts.happy.hippo-1.dev.lclclerk.com":                    false,
		"https://happy-hippo-1.lclclerk.com":                         false,
		"":                                                           false,
		"     ":                                                      false,
		" fndjf*#(@)! ":                                              false,
	}

	for input, expected := range testcases {
		input, expected := input, expected

		t.Run(input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, expected, UsesNewFapiDevDomain(input))
		})
	}
}
