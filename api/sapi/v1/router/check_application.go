package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
)

func checkUpdateOnSystemApplication(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	env := environment.FromContext(r.Context())
	if clerkhttp.IsMutationMethod(r.Method) && env.Application.Type == string(constants.RTSystem) {
		return r, apierror.CannotUpdateUserLimitsOnProductionInstance()
	}
	return r, nil
}

func notDeleted(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	env := environment.FromContext(r.Context())
	if env.Application.HardDeleteAt.Valid {
		return r, apierror.ResourceNotFound()
	}
	return r, nil
}
