package instance_organization_permissions

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{service: NewService(deps)}
}

// GET /v1/organization_permissions
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		pagination: paginationParams,
		query:      clerkhttp.GetOptionalQueryParam(r, "query"),
		orderBy:    clerkhttp.GetOptionalQueryParam(r, "order_by"),
	}

	return h.service.List(r.Context(), params)
}
