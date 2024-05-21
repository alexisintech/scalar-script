package billing_change_plan

import (
	"context"
)

type SerializedBillingChangePlan20210205 struct{}

func newSerializedBillingChangePlan20210205() Serializer {
	return &SerializedBillingChangePlan20210205{}
}

func (s *SerializedBillingChangePlan20210205) Serialize(_ context.Context, redirectURL string) *SerializedBillingChangePlan {
	return &SerializedBillingChangePlan{
		Object:      ObjectName,
		RedirectURL: redirectURL,
	}
}
