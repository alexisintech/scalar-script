package restrictions

import (
	"testing"

	"clerk/pkg/constants"

	"github.com/stretchr/testify/assert"
)

func TestIsRestrictedSubaddress(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		identifier string
		testMode   bool
		want       bool
		message    string
	}{
		{"homer+1@simpsons.com", true, true, "test mode subaddress +"},
		{"homer=1@simpsons.com", true, true, "test mode subaddress ="},
		{"homer#1@simpsons.com", true, true, "test mode subaddress #"},
		{"homer+clerk_test@simpsons.com", true, false, "test mode test subaddress +"},
		{"homer=clerk_test@simpsons.com", true, true, "test mode test subaddress ="},
		{"homer#clerk_test@simpsons.com", true, true, "test mode test subaddress #"},
		{"homer+clerk_test@simpsons.com", false, true, "test subaddress non test mode"},
		{"homer@simpsons.com", false, false, "no subaddress"},
		{"homer@si+mpsons.com", false, false, "domain part +"},
		{"homer-1@simpsons.com", false, false, "local part contains hyphen"},
		{"homer.1@simpsons.com", false, false, "local part contains dot"},
		{"homer(1)@simpsons.com", false, false, "local part contains comments"},
		{"homer@simpsons.com(1)", false, false, "domain contains comments"},
	} {
		ident := Identification{
			Identifier: tc.identifier,
			Type:       constants.ITEmailAddress,
		}
		got := isRestrictedSubaddress(ident, tc.testMode)
		assert.Equal(t, tc.want, got, tc.message)
	}
}
