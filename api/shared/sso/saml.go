// Package sso provides single sign-on related functionality.
package sso

import (
	"clerk/pkg/saml"
	"clerk/pkg/saml/provider"
)

// RegisterSAMLProviders enables our currently supported SAML IdP providers.
func RegisterSAMLProviders() {
	saml.RegisterProviders(
		provider.Custom{},
		provider.Okta{},
		provider.Microsoft{},
		provider.Google{},
	)
}
