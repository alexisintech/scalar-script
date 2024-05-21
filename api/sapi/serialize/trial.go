package serialize

import "clerk/model"

type TrialResponse struct {
	ApplicationID         string `json:"application_id"`
	ApplicationName       string `json:"application_name"`
	SubscriptionTrialDays int    `json:"subscription_trial_days"`
}

func Trial(application *model.Application, subscription *model.Subscription) *TrialResponse {
	return &TrialResponse{
		ApplicationID:         application.ID,
		ApplicationName:       application.Name,
		SubscriptionTrialDays: subscription.TrialPeriodDays,
	}
}
