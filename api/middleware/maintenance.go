package middleware

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/cenv"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/recovery"
)

func SetMaintenanceAndRecoveryMode(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	newCtx := maintenance.NewContext(r.Context(), cenv.IsEnabled(cenv.ClerkMaintenanceMode))
	newCtx = recovery.NewContext(newCtx, cenv.IsEnabled(cenv.ClerkRecoveryMode))
	return r.WithContext(newCtx), nil
}
