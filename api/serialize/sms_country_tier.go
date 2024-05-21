package serialize

type SMSCountryTierResponse struct {
	CountryCode string `json:"country_code"`
	Tier        string `json:"tier"`
	UnitPrice   int    `json:"unit_price"`
}

func SMSCountryTier(countryCode, tier string, unitPrice int) *SMSCountryTierResponse {
	return &SMSCountryTierResponse{
		CountryCode: countryCode,
		Tier:        tier,
		UnitPrice:   unitPrice,
	}
}
