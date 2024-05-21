package auth_config

import (
	"clerk/model"
	"clerk/repository"
)

type Service struct {
	authConfigRepo *repository.AuthConfig
}

func NewService() *Service {
	return &Service{
		authConfigRepo: repository.NewAuthConfig(),
	}
}

func (s Service) UpdateUserSettingsWithProgressiveSignUp(authConfig *model.AuthConfig, progressiveSignUp bool) {
	authConfig.UserSettings.SignUp.Progressive = progressiveSignUp

	// Given the same usersettings, PSU (by design) behaves differently
	// than non-PSU. To ease DX, we try to preserve the resulting
	// behavior as much as possible by tweaking the settings when moving
	// to/from PSU.
	web3 := &authConfig.UserSettings.Attributes.Web3Wallet
	email := &authConfig.UserSettings.Attributes.EmailAddress
	phone := &authConfig.UserSettings.Attributes.PhoneNumber

	if progressiveSignUp { // migrating to PSU
		if web3.Enabled {
			// web3 was always behaving as optional in non-PSU
			web3.Required = false
		}

		// in non-PSU, UsedForFirstFactor indicated whether the
		// email/phone fields were actually required
		if email.Enabled && email.UsedForFirstFactor {
			email.Required = true
		}
		if phone.Enabled && phone.UsedForFirstFactor {
			phone.Required = true
		}
	} else { // migrating to non-PSU
		if web3.Enabled {
			// web3 was always marked as required in non-PSU, even
			// though it behaved as optional
			web3.Required = true
		}

		// in non-PSU, UsedForFirstFactor indicated whether the
		// email/phone fields were actually required
		if email.Enabled && email.Required {
			email.UsedForFirstFactor = true
		}
		if phone.Enabled && phone.Required {
			phone.UsedForFirstFactor = true
		}
	}
}
