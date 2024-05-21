package plain

import (
	"clerk/api/apierror"
	"clerk/pkg/cenv"
	"clerk/utils/clerk"
	"clerk/utils/url"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/team-plain/go-sdk/customercards"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// Middleware factory for all /v1/internal/support-ops/* endpoints
func (h *HTTP) EnsureValidToken(envVarKey string) func(http.ResponseWriter, *http.Request) (*http.Request, apierror.Error) {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		token, err := url.BearerAuthHeader(r)
		if err != nil {
			return nil, err
		}
		if cenv.Get(envVarKey) == "" {
			return nil, apierror.InvalidAuthentication()
		}
		if token != cenv.Get(envVarKey) {
			return nil, apierror.InvalidAuthentication()
		}
		return r, nil
	}
}

// POST /v1/internal/support-ops/plain/customercards
func (h *HTTP) CustomerCards(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var req customercards.Request

	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		// "invalid json request"
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.GetCustomerCard(
		r.Context(),
		req.Customer.Email,
		req.CardKeys,
	)
}

// GET /v1/internal/support-ops/customer-data/{userID}
func (h *HTTP) CustomerData(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	if userID == "" {
		return nil, apierror.BadRequest()
	}

	return h.service.GetCustomerData(r.Context(), userID)
}
