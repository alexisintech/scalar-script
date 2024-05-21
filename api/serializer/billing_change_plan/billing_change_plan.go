package billing_change_plan

import (
	"context"
)

const ObjectName = "checkout_session"

// sharedSerializerFields defines shared fields for all SerializedBillingChangePlan.
// It's for code generation only, not used in code.
//
// nolint:unused
type sharedSerializerFields struct {
	Object      string `json:"object"`
	RedirectURL string `json:"redirect_url"`
}

// sharedSerializerMethods defines shared methods for all Serializer implementations.
// It's for code generation only, not used in code.
//
// nolint:unused
type sharedSerializerMethods interface {
	Serialize(ctx context.Context, redirectURL string) *SerializedBillingChangePlan
}
