// Code generated by apiversioning CLI; DO NOT EDIT.

package serializer

import (
	"clerk/api/serializer/billing_change_plan"
	"clerk/api/serializer/billing_current"
)

type Factory struct {
	billingChangePlan billing_change_plan.Serializer
	billingCurrent    billing_current.Serializer
}

func NewFactory() *Factory {
	return &Factory{}
}

func (f *Factory) BillingChangePlan() billing_change_plan.Serializer {
	if f.billingChangePlan == nil {
		f.billingChangePlan = billing_change_plan.NewSerializer()
	}
	return f.billingChangePlan
}

func (f *Factory) BillingCurrent() billing_current.Serializer {
	if f.billingCurrent == nil {
		f.billingCurrent = billing_current.NewSerializer()
	}
	return f.billingCurrent
}