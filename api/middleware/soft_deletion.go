package middleware

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/environment"
)

func EnsureEnvNotPendingDeletion(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	env := environment.FromContext(r.Context())
	if env.Application.HardDeleteAt.Valid {
		return r, apierror.ResourceNotFound()
	}
	return r, nil
}
