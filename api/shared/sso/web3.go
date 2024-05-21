package sso

import (
	"clerk/pkg/web3"
	"clerk/pkg/web3/provider"
)

// RegisterWeb3Providers enables our currently supported web3 providers
func RegisterWeb3Providers() {
	web3.RegisterProviders(
		provider.MetaMask{},
	)
}
