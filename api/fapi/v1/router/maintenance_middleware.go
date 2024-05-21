package router

import (
	"net/http"
	"strings"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/maintenance"
)

func blockDuringMaintenance(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	if maintenance.FromContext(r.Context()) {
		return r, apierror.SystemUnderMaintenance()
	}
	return r, nil
}

func checkRequestAllowedDuringMaintenance(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	if !maintenance.FromContext(ctx) {
		return r, nil
	}

	if !clerkhttp.IsMutationMethod(r.Method) {
		// if it's not a mutation request, let it through
		return r, nil
	}

	// allow mutation for certain endpoints necessary for
	// session token refresh
	if mutationIsAllowedOnPath(r.URL.Path) {
		return r, nil
	}
	return r, apierror.SystemUnderMaintenance()
}

var allowedMutationOnPathSuffixes = []string{
	"/touch",
	"/tokens",
}

func mutationIsAllowedOnPath(path string) bool {
	if strings.HasPrefix(path, "/v1/dev_browser") {
		return true
	}
	if !strings.HasPrefix(path, "/v1/client/sessions") {
		return false
	}
	for _, suffix := range allowedMutationOnPathSuffixes {
		if strings.Contains(path, suffix) {
			return true
		}
	}
	return false
}
