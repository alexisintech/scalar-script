package environment

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/constants"
	clerkstrings "clerk/pkg/strings"
	"clerk/utils/database"
	"clerk/utils/url"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

// Middleware /v1
func (h *HTTP) SetEnvironmentFromHeader(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	secretKey, err := url.BearerAuthHeader(r)
	if err != nil {
		return nil, err
	}

	// Add secret key prefix in case it is missing (legacy key)
	if secretKey != "" {
		secretKey = clerkstrings.AddPrefixIfNeeded(secretKey, constants.SecretKeyPrefix)
	}

	newCtx, err := h.service.SetEnvironmentFromKey(r.Context(), secretKey)
	if err != nil {
		return nil, err
	}

	return r.WithContext(newCtx), nil
}
