// Code generated by apiversioning CLI; DO NOT EDIT.

package billing_change_plan

import (
	"context"

	"clerk/pkg/apiversioning"
	apiversioningcontext "clerk/pkg/apiversioning/context"
)

// SerializedBillingChangePlan fields is derived from sharedSerializerFields.
type SerializedBillingChangePlan struct {
	Object      string `json:"object"`
	RedirectURL string `json:"redirect_url"`
}

// Serializer fields is derived from sharedSerializerMethods.
type Serializer interface {
	Serialize(ctx context.Context, redirectURL string) *SerializedBillingChangePlan
}

type serializer struct {
	SerializedBillingChangePlan20210205 Serializer
}

func NewSerializer() Serializer {
	return &serializer{}
}

func (s *serializer) Serialize(ctx context.Context, redirectURL string) *SerializedBillingChangePlan {
	activeSerializer := s.getSerializerForVersion(ctx)
	return activeSerializer.Serialize(ctx, redirectURL)
}

func (s *serializer) getSerializerForVersion(ctx context.Context) Serializer {
	v, _ := apiversioningcontext.FromContext(ctx)

	if v.GTE(apiversioning.V20210205) {
		if s.SerializedBillingChangePlan20210205 == nil {
			s.SerializedBillingChangePlan20210205 = newSerializedBillingChangePlan20210205()
		}
		return s.SerializedBillingChangePlan20210205
	}

	return newSerializedBillingChangePlan20210205()
}