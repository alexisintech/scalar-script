package router

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/maintenance"
)

func checkRequestAllowedDuringMaintenance(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if maintenance.FromContext(r.Context()) && clerkhttp.IsMutationMethod(r.Method) {
		return r, apierror.SystemUnderMaintenance()
	}
	return r, nil
}
