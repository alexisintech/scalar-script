package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/validation"
)

func validateUserSettings(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	userSettings := clerk.NewUserSettings(env.AuthConfig.UserSettings)

	if err := validation.Validate(userSettings); err != nil {
		return r, apierror.InvalidUserSettings()
	}
	return r, nil
}
