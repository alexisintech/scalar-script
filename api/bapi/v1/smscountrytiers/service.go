package smscountrytiers

import (
	"clerk/api/serialize"
	"clerk/model"
	"clerk/pkg/billing"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"context"
)

type Service struct {
	smsCountryTiersRepo   *repository.SMSCountryTiers
	subscriptionPriceRepo *repository.SubscriptionPrices
	db                    database.Database
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		smsCountryTiersRepo: repository.NewSMSCountryTiers(),
		db:                  deps.DB(),
	}
}

func (s *Service) GetCountryTiers(ctx context.Context) ([]*serialize.SMSCountryTierResponse, error) {
	countryTiers, err := s.smsCountryTiersRepo.FindAll(ctx, s.db)
	if err != nil {
		return nil, err
	}

	proPlanPrices, err := s.subscriptionPriceRepo.FindAllActiveBySubscriptionPlanID(ctx, s.db, constants.PricingProSubscriptionPlanID)
	if err != nil {
		return nil, err
	}

	pricesPerTier := priceTypesToSMSTiers(proPlanPrices)
	serializedCountryTiers := make([]*serialize.SMSCountryTierResponse, 0, len(countryTiers))
	for _, countryTier := range countryTiers {
		if err != nil {
			return nil, err
		}

		price, ok := pricesPerTier[countryTier.Tier]
		if !ok {
			return nil, clerkerrors.WithStacktrace("services/sms_country_tiers: price not found for tier %s", countryTier.Tier)
		}
		serializedCountryTier := serialize.SMSCountryTier(countryTier.CountryCode, countryTier.Tier, price)
		serializedCountryTiers = append(serializedCountryTiers, serializedCountryTier)
	}

	return serializedCountryTiers, nil
}

func priceTypesToSMSTiers(proPrices []*model.SubscriptionPrice) map[string]int {
	prices := make(map[string]int)
	for _, proPrice := range proPrices {
		tier := getTierForPrice(proPrice)
		if tier != "" {
			prices[tier] = proPrice.UnitAmount
		}
	}

	return prices
}

// getTierForPrice finds and returns the tier for the given subscription price.
// If the metric does not correspond to a sms tier but is some other type of
// resource, an empty string is returned.
func getTierForPrice(subscriptionPrice *model.SubscriptionPrice) string {
	switch subscriptionPrice.Metric {
	case billing.PriceTypes.SMSMessagesTierA:
		return constants.SMSTierAAggregationType
	case billing.PriceTypes.SMSMessagesTierB:
		return constants.SMSTierBAggregationType
	case billing.PriceTypes.SMSMessagesTierC:
		return constants.SMSTierCAggregationType
	case billing.PriceTypes.SMSMessagesTierD:
		return constants.SMSTierDAggregationType
	case billing.PriceTypes.SMSMessagesTierE:
		return constants.SMSTierEAggregationType
	case billing.PriceTypes.SMSMessagesTierF:
		return constants.SMSTierFAggregationType
	}
	return ""
}
