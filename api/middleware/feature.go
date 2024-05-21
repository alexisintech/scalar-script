package middleware

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
)

// EnabledInUserSettings checks whether the feature that is being accessed is enabled on the instance
func EnabledInUserSettings(feature names.AttributeName) func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		env := environment.FromContext(r.Context())

		userSettings := clerk.NewUserSettings(env.AuthConfig.UserSettings)
		attribute := userSettings.GetAttribute(feature)

		if !attribute.Base().Enabled {
			return r, apierror.FeatureNotEnabled()
		}

		return r, nil
	}
}
