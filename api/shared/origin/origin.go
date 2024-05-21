package origin

import (
	"strings"

	"clerk/pkg/cenv"
	"clerk/pkg/set"
)

func ValidateDashboardOrigin(origin string) bool {
	if cenv.IsTest() {
		return origin == "https://clerktest.com" || origin == "https://dashboard.clerktest.com"
	}

	if cenv.IsDevelopment() {
		return strings.HasSuffix(origin, ".prod.lclclerk.com")
	}

	if cenv.IsStaging() {
		stagingDashboardTLDS := set.New(cenv.GetStringList(cenv.ClerkStagingDashboardTLDS)...)

		for _, tld := range stagingDashboardTLDS.Array() {
			if strings.HasSuffix(origin, tld) {
				return true
			}
		}

		return false
	}

	if cenv.IsProduction() {
		return (origin == "https://dashboard.clerk.com" || origin == "https://clerk.com")
	}
	return false
}
