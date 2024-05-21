package system_config

import (
	"clerk/api/dapi/serialize"
	"clerk/pkg/cenv"
	"clerk/pkg/web3"
	"clerk/repository"
	"clerk/utils/database"

	"clerk/api/apierror"
	"clerk/pkg/billing"
	"clerk/pkg/oauth"
)

type Service struct {
	db                 database.Database
	planRepo           *repository.SubscriptionPlans
	samlConnectionRepo *repository.SAMLConnection
}

func NewService(db database.Database) *Service {
	return &Service{
		db:                 db,
		planRepo:           repository.NewSubscriptionPlans(),
		samlConnectionRepo: repository.NewSAMLConnection(),
	}
}

// Read returns system-wide configuration
func (s *Service) Read() (*serialize.SystemConfigResponse, apierror.Error) {
	// We filter out OAuth providers for which we can't offer dev credentials, as users shouldn't be able
	// to enable them during application creation, or when they are deprecated.
	oauthProviders := make([]string, 0)
	for _, id := range oauth.Providers() {
		if !oauth.DevCredentialsAvailable(id) {
			continue
		}

		provider, err := oauth.GetProvider(id)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		if provider.IsDeprecated() {
			continue
		}

		oauthProviders = append(oauthProviders, id)
	}

	featureFlags := serialize.SystemFeatureFlags{
		AllowNewPricingCheckout:  cenv.GetBool(cenv.FlagAllowNewPricingCheckout),
		AllowOrganizationBilling: cenv.GetBool(cenv.FlaAllowOrganizationBilling),
	}

	notifications := serialize.Notifications{}

	return serialize.SystemConfig(oauthProviders, web3.Providers(), billing.AllFeatures(), featureFlags, notifications), nil
}
