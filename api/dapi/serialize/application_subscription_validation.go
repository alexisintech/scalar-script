package serialize

type ApplicationSubscriptionValidationResponse struct {
	UnsupportedFeatures []string `json:"unsupported_features"`
	RefundAmount        int64    `json:"refund_amount"`
}

func ApplicationSubscriptionValidation(unsupportedFeatures []string, refundAmount int64) *ApplicationSubscriptionValidationResponse {
	return &ApplicationSubscriptionValidationResponse{
		UnsupportedFeatures: unsupportedFeatures,
		RefundAmount:        refundAmount,
	}
}
