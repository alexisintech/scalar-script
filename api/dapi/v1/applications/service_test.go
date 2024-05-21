package applications

import (
	"testing"
	"time"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"

	"github.com/stretchr/testify/assert"
	"github.com/volatiletech/null/v8"
)

func TestSortPlans(t *testing.T) {
	t.Parallel()

	first := createPlan("first", 10, nil)
	second := createPlan("second", 20, nil)
	third := createPlan("third", 30, nil)

	// create some hierarchy
	free := createPlan("free", 100, nil)
	hobby := createPlan("hobby", 200, &free.ID)
	business := createPlan("business", 300, &hobby.ID)

	plans := []*model.SubscriptionPlan{
		business,
		hobby,
		first,
		third,
		second,
		free,
	}

	sortPlans(plans, buildPlanHierarchy(plans))

	assert.Equal(t, plans[0], first)
	assert.Equal(t, plans[1], second)
	assert.Equal(t, plans[2], third)
	assert.Equal(t, plans[3], free)
	assert.Equal(t, plans[4], hobby)
	assert.Equal(t, plans[5], business)
}

func TestDetermineAction(t *testing.T) {
	t.Parallel()

	// create some hierarchy
	freeJune := createPlan("freeJune", 0, nil)
	hobbyJune := createPlan("hobbyJune", 0, &freeJune.ID)
	businessJune := createPlan("businessJune", 0, &hobbyJune.ID)

	// create another hierarchy
	hobbyAugust := createPlan("hobbyAugust", 0, nil)
	businessAugust := createPlan("businessAugust", 0, &hobbyAugust.ID)

	planHierarchy := buildPlanHierarchy([]*model.SubscriptionPlan{
		freeJune,
		hobbyJune,
		businessJune,
		hobbyAugust,
		businessAugust,
	})

	assert.Equal(t, "downgrade", determineAction([]*model.SubscriptionPlan{businessJune}, hobbyJune.ID, planHierarchy))
	assert.Equal(t, "downgrade", determineAction([]*model.SubscriptionPlan{businessJune}, freeJune.ID, planHierarchy))
	assert.Equal(t, "downgrade", determineAction([]*model.SubscriptionPlan{hobbyJune}, freeJune.ID, planHierarchy))
	assert.Equal(t, "downgrade", determineAction([]*model.SubscriptionPlan{businessAugust}, hobbyAugust.ID, planHierarchy))

	assert.Equal(t, "upgrade", determineAction([]*model.SubscriptionPlan{freeJune}, hobbyJune.ID, planHierarchy))
	assert.Equal(t, "upgrade", determineAction([]*model.SubscriptionPlan{freeJune}, businessJune.ID, planHierarchy))
	assert.Equal(t, "upgrade", determineAction([]*model.SubscriptionPlan{hobbyJune}, businessJune.ID, planHierarchy))
	assert.Equal(t, "upgrade", determineAction([]*model.SubscriptionPlan{hobbyAugust}, businessAugust.ID, planHierarchy))

	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{freeJune}, hobbyAugust.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{freeJune}, businessAugust.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{hobbyJune}, hobbyAugust.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{hobbyJune}, businessAugust.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{businessJune}, hobbyAugust.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{businessJune}, businessAugust.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{hobbyAugust}, freeJune.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{hobbyAugust}, hobbyJune.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{hobbyAugust}, businessJune.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{businessAugust}, freeJune.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{businessAugust}, hobbyJune.ID, planHierarchy))
	assert.Equal(t, "switch", determineAction([]*model.SubscriptionPlan{businessAugust}, businessJune.ID, planHierarchy))

	assert.Empty(t, determineAction([]*model.SubscriptionPlan{freeJune}, freeJune.ID, planHierarchy))
}

func createPlan(id string, userLimit int, basePlan *string) *model.SubscriptionPlan {
	return &model.SubscriptionPlan{
		SubscriptionPlan: &sqbmodel.SubscriptionPlan{
			ID:               id,
			Scope:            constants.ApplicationResource,
			MonthlyUserLimit: userLimit,
			CreatedAt:        time.Now(),
			BasePlan:         null.StringFromPtr(basePlan),
		},
	}
}
