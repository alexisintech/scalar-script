package serialize

import (
	"clerk/model"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/set"
)

type InstanceFeaturesResponse struct {
	Key             string                    `json:"key"`
	IsSupported     bool                      `json:"is_supported"`
	IsInGracePeriod bool                      `json:"is_in_grace_period"`
	Plan            *SubscriptionPlanResponse `json:"plan"`
}

func InstanceFeatures(env *model.Env, enabledPlans, availablePlans []*model.SubscriptionPlan) map[string]*InstanceFeaturesResponse {
	availablePlansMap := make(map[string]*model.SubscriptionPlan)
	for _, plan := range availablePlans {
		availablePlansMap[plan.ID] = plan
	}

	planHierarchy := make(map[string]string)
	for _, plan := range availablePlans {
		if plan.BasePlan.Valid {
			planHierarchy[plan.ID] = plan.BasePlan.String
		}
	}

	currentPlan := billing.DetectCurrentPlan(enabledPlans)
	eligiblePlans := determineEligiblePlans(currentPlan, availablePlans, planHierarchy)
	enabledFeatures := set.New[string]()
	for _, plan := range enabledPlans {
		enabledFeatures.Insert(plan.Features...)
	}
	// XXX Account for features that were on the free plan before our
	// Pricing V2 release.
	if cenv.IsBeforeCutoff(cenv.PricingV2PaidFeaturesCutoffEpochTime, env.Instance.CreatedAt) {
		enabledFeatures.Insert(billing.FeaturesThatMovedToPaidAfterPricingV2()...)
	}

	// aggregate features from all eligible plans and addons
	planFeaturesMap := make(map[string]set.Set[string])
	availableFeatures := set.New(billing.AllFeatures()...)
	eligibleAddonPlans := make([]*model.SubscriptionPlan, 0)
	for _, eligiblePlan := range eligiblePlans {
		availableFeatures.Insert(eligiblePlan.Features...)
		planFeaturesMap[eligiblePlan.ID] = set.New[string](eligiblePlan.Features...)
		for _, addonID := range eligiblePlan.Addons {
			if addonPlan, ok := availablePlansMap[addonID]; ok {
				eligibleAddonPlans = append(eligibleAddonPlans, addonPlan)
				availableFeatures.Insert(addonPlan.Features...)
			}
		}
	}
	allEligiblePlans := append(eligibleAddonPlans, eligiblePlans...)

	gracePeriodFeatures := set.New[string](env.Subscription.GracePeriodFeatures...)
	featuresRespMap := make(map[string]*InstanceFeaturesResponse)
	for _, feature := range availableFeatures.Array() {
		isSupported := env.Instance.HasAccessToAllFeatures() || enabledFeatures.Contains(feature)
		isInGracePeriod := !env.Instance.HasAccessToAllFeatures() && gracePeriodFeatures.Contains(feature)

		featuresResp := &InstanceFeaturesResponse{
			Key:             feature,
			IsSupported:     isSupported,
			IsInGracePeriod: isInGracePeriod,
		}

		if !enabledFeatures.Contains(feature) {
			for _, plan := range allEligiblePlans {
				if planFeaturesMap[plan.ID].Contains(feature) {
					featuresResp.Plan = SubscriptionPlan(plan)
					break
				}
			}
		}

		featuresRespMap[feature] = featuresResp
	}

	return featuresRespMap
}

// determineEligiblePlans builds a list of eligible plans based on the current plan's visibility
func determineEligiblePlans(currentPlan *model.SubscriptionPlan, availablePlans []*model.SubscriptionPlan, planHierarchy map[string]string) []*model.SubscriptionPlan {
	availablePlansMap := make(map[string]*model.SubscriptionPlan)
	visiblePlans := make([]*model.SubscriptionPlan, 0)
	for _, plan := range availablePlans {
		availablePlansMap[plan.ID] = plan
		if plan.Visible {
			visiblePlans = append(visiblePlans, plan)
		}
	}

	var eligiblePlans []*model.SubscriptionPlan

	// find the next plan that is visible in the hierarchy, if current is not
	nextVisiblePlan := getNextVisiblePlan(currentPlan, availablePlansMap, planHierarchy)
	if nextVisiblePlan != nil {
		eligiblePlans = append(eligiblePlans, nextVisiblePlan)
	}

	for _, plan := range visiblePlans {
		if plan.ID != currentPlan.ID {
			eligiblePlans = append(eligiblePlans, plan)
		}
	}

	return eligiblePlans
}

// getNextVisiblePlan finds the next visible plan from the current plan
func getNextVisiblePlan(currentPlan *model.SubscriptionPlan, availablePlansMap map[string]*model.SubscriptionPlan, planHierarchy map[string]string) *model.SubscriptionPlan {
	if currentPlan.Visible {
		return currentPlan
	}

	// traverse up in the plan hierarchy to find the first visible base plan
	for baseID := currentPlan.BasePlan.String; baseID != ""; baseID = planHierarchy[baseID] {
		if basePlan, ok := availablePlansMap[baseID]; ok && basePlan.Visible {
			return basePlan
		}
	}

	return nil
}
