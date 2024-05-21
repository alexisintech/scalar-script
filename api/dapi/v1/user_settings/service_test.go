package user_settings

import (
	"testing"

	"clerk/pkg/usersettings/model"

	"github.com/stretchr/testify/assert"
)

func TestSetZeroValuesForDisabledAttributes(t *testing.T) {
	t.Parallel()

	userSettingsToModify := model.UserSettings{
		Attributes: model.Attributes{
			EmailAddress: model.Attribute{
				Enabled:            false,
				UsedForFirstFactor: true,
				FirstFactors:       []string{"random"},
			},
			PhoneNumber: model.Attribute{
				Enabled:            true,
				UsedForFirstFactor: true,
				FirstFactors:       []string{"random"},
			},
		},
	}

	cleanedUpUserSettings := setZeroValuesForDisabledAttributes(&userSettingsToModify)

	assert.False(t, cleanedUpUserSettings.Attributes.EmailAddress.Enabled)
	assert.False(t, cleanedUpUserSettings.Attributes.EmailAddress.UsedForFirstFactor)
	assert.Equal(t, []string{}, cleanedUpUserSettings.Attributes.EmailAddress.FirstFactors)

	assert.True(t, cleanedUpUserSettings.Attributes.PhoneNumber.Enabled)
	assert.True(t, cleanedUpUserSettings.Attributes.PhoneNumber.UsedForFirstFactor)
	assert.Equal(t, []string{"random"}, cleanedUpUserSettings.Attributes.PhoneNumber.FirstFactors)
}
