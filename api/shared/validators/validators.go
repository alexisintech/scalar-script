package validators

import (
	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/constants"
	usersettings "clerk/pkg/usersettings/clerk"
)

// ValidateEnhancedEmailDeliverability returns an error if enhanced email
// deliverability is on and the user settings support magic links.
// Enhanced email deliverability is not available for magic links.
func ValidateEnhancedEmailDeliverability(isOn bool, userSettings *usersettings.UserSettings) apierror.Error {
	hasEmailLink := userSettings.FirstFactors().Contains(constants.VSEmailLink) ||
		userSettings.VerificationStrategies().Contains(constants.VSEmailLink)
	if isOn && hasEmailLink {
		return apierror.EnhancedEmailDeliverabilityProhibited()
	}

	return nil
}

func ValidateCAPTCHASetting(instance *model.Instance, settings *usersettings.UserSettings) apierror.Error {
	if settings.SignUp.CaptchaEnabled && !instance.IsProduction() {
		return apierror.InvalidUserSettings()
	}

	widgetType := settings.SignUp.CaptchaWidgetType
	if widgetType != "" && !constants.TurnstileWidgetTypes.Contains(widgetType) {
		return apierror.InvalidCaptchaWidgetType(string(settings.SignUp.CaptchaWidgetType))
	}

	return nil
}
