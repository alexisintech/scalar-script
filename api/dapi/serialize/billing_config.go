package serialize

import (
	"clerk/model"
)

type BillingConfigResponse struct {
	AccountID     string   `json:"account_id"`
	CustomerTypes []string `json:"customer_types"`
	PortalEnabled bool     `json:"portal_enabled"`
}

func BillingConfig(instance *model.Instance) *BillingConfigResponse {
	return &BillingConfigResponse{
		AccountID:     instance.ExternalBillingAccountID.String,
		CustomerTypes: instance.BillingCustomerTypes,
		PortalEnabled: instance.BillingPortalEnabled.Bool,
	}
}
