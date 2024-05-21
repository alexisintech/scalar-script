// Package kima_hosts implements utilities related to the new FAPI development
// hosts introduced with project Kima.
//
// The new FAPI hostnames are of the following format:
// `<random-part>.clerk.<shared-dev-domain>` instead of
// `clerk.<random-part>.<shared-dev-domain>`. For example, an instance that
// would previously be accessed via:
//
//	clerk.happy.hippo-1.lcl.dev
//
// can now be accessed via:
//
//	happy-hippo-1.clerk.accounts.dev
//
// Note that we introduced a new root domain, accounts.dev, that replaces the
// lcl.dev domain in the new hostnames. Essentially, the hostnames change as
// follows:
//
// - FAPI: clerk.happy.hippo-1.lcl.dev becomes happy-hippo-1.clerk.accounts.dev
// - Accounts: accounts.happy.hippo-1.lcl.dev becomes happy-hippo-1.accounts.dev
//
// An instance's FAPI can operate via either of the previous and new hostnames
// and the decision is taken based on the request's FAPI hostname
package kima_hosts

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"

	"clerk/pkg/cenv"
)

// UsesNewFapiDevDomain returns true if the incoming request uses the new FAPI
// hosts.
func UsesNewFapiDevDomain(hostOrURL string) bool {
	if hostOrURL == "" {
		return false
	}

	publishableKeySharedDevDomain := cenv.Get(cenv.ClerkPublishableKeySharedDevDomain)
	if publishableKeySharedDevDomain == "" {
		return false
	}

	u, err := url.ParseRequestURI(hostOrURL)
	if err == nil {
		return strings.HasSuffix(u.Hostname(), publishableKeySharedDevDomain)
	}

	return strings.HasSuffix(hostOrURL, publishableKeySharedDevDomain)
}

func FapiDomainByPublishableKey(publishableKey string) (string, error) {
	// pk_<test|live>_xxxxxx
	parts := strings.Split(publishableKey, "_")
	if len(parts) != 3 {
		return "", fmt.Errorf("malformed publishable key (%s)", publishableKey)
	}

	// happy-hippo-1.clerk.accounts.dev$
	// clerk.example.com$
	decoded, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("malformed publishable key (%s): %w", publishableKey, err)
	}

	if !strings.HasSuffix(string(decoded), "$") {
		return "", fmt.Errorf("malformed publishable key (%s)", decoded)
	}

	return strings.TrimSuffix(string(decoded), "$"), nil
}
