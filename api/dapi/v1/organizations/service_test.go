package organizations

import (
	"testing"

	"clerk/model"
	"clerk/model/sqbmodel"

	"github.com/stretchr/testify/assert"
)

func TestDetermineAction(t *testing.T) {
	t.Parallel()

	limitedThreeSeatsPlan := &model.SubscriptionPlan{
		SubscriptionPlan: &sqbmodel.SubscriptionPlan{
			ID:                          "three-seats",
			OrganizationMembershipLimit: 3,
		},
	}

	limitedFiveSeatsPlan := &model.SubscriptionPlan{
		SubscriptionPlan: &sqbmodel.SubscriptionPlan{
			ID:                          "five-seats",
			OrganizationMembershipLimit: 5,
		},
	}

	unlimitedSeatsPlan := &model.SubscriptionPlan{
		SubscriptionPlan: &sqbmodel.SubscriptionPlan{
			ID:                          "unlimited-seats",
			OrganizationMembershipLimit: 0,
		},
	}

	anotherUnlimitedSeatsPlan := &model.SubscriptionPlan{
		SubscriptionPlan: &sqbmodel.SubscriptionPlan{
			ID:                          "another-unlimited-seats",
			OrganizationMembershipLimit: 0,
		},
	}

	assert.Equal(t, "upgrade", determineAction(limitedThreeSeatsPlan, limitedFiveSeatsPlan))
	assert.Equal(t, "upgrade", determineAction(limitedThreeSeatsPlan, unlimitedSeatsPlan))

	assert.Equal(t, "downgrade", determineAction(limitedFiveSeatsPlan, limitedThreeSeatsPlan))
	assert.Equal(t, "upgrade", determineAction(limitedFiveSeatsPlan, unlimitedSeatsPlan))

	assert.Equal(t, "downgrade", determineAction(unlimitedSeatsPlan, limitedThreeSeatsPlan))
	assert.Equal(t, "downgrade", determineAction(unlimitedSeatsPlan, limitedFiveSeatsPlan))

	assert.Equal(t, "", determineAction(unlimitedSeatsPlan, unlimitedSeatsPlan))
	assert.Equal(t, "switch", determineAction(unlimitedSeatsPlan, anotherUnlimitedSeatsPlan))
}
