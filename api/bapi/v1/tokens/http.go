package tokens

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// POST /v1/tokens
func (h *HTTP) CreateFromTemplate(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateFromTemplateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.CreateFromTemplate(r.Context(), params)
}
