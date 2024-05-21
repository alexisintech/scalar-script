package jwks

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/ctx/environment"
)

type HTTP struct{}

func NewHTTP() *HTTP {
	return &HTTP{}
}

func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	env := environment.FromContext(r.Context())

	jwks, err := env.Instance.JWKS()
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return jwks, nil
}
