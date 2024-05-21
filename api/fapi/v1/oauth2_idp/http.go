package oauth2_idp

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/oauth2idp"
	"clerk/utils/clerk"
	"clerk/utils/url"
)

type HTTP struct {
	service  *Service
	provider *oauth2idp.Provider
}

func NewHTTP(deps clerk.Deps) *HTTP {
	provider := oauth2idp.New(deps)
	return &HTTP{
		service:  NewService(deps),
		provider: provider,
	}
}

func (h *HTTP) SetUserFromClient(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	newCtx, err := h.service.SetUserFromClient(r.Context())
	if err != nil {
		return nil, err
	}
	return r.WithContext(newCtx), err
}

func (h *HTTP) SetUserFromAccessToken(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	accessToken, err := url.BearerAuthHeader(r)
	if err != nil {
		return nil, err
	}

	newCtx, err := h.service.SetUserFromAccessToken(r.Context(), accessToken)
	if err != nil {
		return nil, err
	}

	return r.WithContext(newCtx), nil
}

// GET /v1/oauth/authorize
func (h *HTTP) Authorize(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if r.Form.Get("scope") == "" {
		r.Form.Set("scope", oauth2idp.FormattedDefaultScopes())
	}

	err := h.provider.Server.HandleAuthorizeRequest(w, r)
	if err != nil {
		if apiErr, isAPIErr := apierror.As(err); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(err)
	}
	return nil, nil
}

// POST /v1/oauth/token
func (h *HTTP) Token(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := h.provider.Server.HandleTokenRequest(w, r)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return nil, nil
}

// GET /v1/oauth/userinfo
func (h *HTTP) UserInfo(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.UserInfo(r.Context())
}
