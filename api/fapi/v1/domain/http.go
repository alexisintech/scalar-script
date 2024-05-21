package domain

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/utils/database"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

// Middleware
func (h *HTTP) SetDomainFromHost(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	newCtx, err := h.service.SetDomainFromRequest(r.Context(), SetDomainParams{
		Host:               r.Header.Get(constants.XOriginalHost),
		ProxyURL:           r.Header.Get(constants.ClerkProxyURL),
		SecretKey:          r.Header.Get(constants.ClerkSecretKey),
		DevSatelliteDomain: r.URL.Query().Get(constants.DomainQueryParam),
	})
	if err != nil {
		return nil, err
	}
	// The __domain query parameter has been consumed and not needed any
	// more. Delete it so that it doesn't leak down to the handlers.
	q := r.URL.Query()
	q.Del(constants.DomainQueryParam)
	r.URL.RawQuery = q.Encode()
	return r.WithContext(newCtx), err
}

// Middleware
func (h *HTTP) EnsurePrimaryDomain(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	env := environment.FromContext(r.Context())
	if env.Domain.IsSatellite(env.Instance) {
		return r, apierror.OperationNotAllowedOnSatelliteDomain()
	}
	return r, nil
}
