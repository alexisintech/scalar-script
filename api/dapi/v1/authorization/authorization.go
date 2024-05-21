package authorization

import (
	"clerk/pkg/billing"
	"clerk/pkg/constants"
	"clerk/pkg/set"
)

var featureAccess = map[string]set.Set[string]{
	constants.RoleAdmin:       set.New(billing.Features.Impersonation),
	constants.RoleBasicMember: set.New[string](),
}

func HasAccess(role, feature string) bool {
	if role == "" {
		// no role means that user is not part of an organization, which automatically
		// makes them the admin of their personal workspace
		return true
	}

	features, roleExists := featureAccess[role]
	if !roleExists {
		return false
	}
	return features.Contains(feature)
}

func AccessibleFeatures(role string) []string {
	if role == "" {
		// personal workspace, this role has access to all features
		return []string{billing.Features.Impersonation}
	}
	accessibleFeatures, roleExists := featureAccess[role]
	if !roleExists {
		return []string{}
	}
	return accessibleFeatures.Array()
}
