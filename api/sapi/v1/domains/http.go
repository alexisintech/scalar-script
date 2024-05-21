package domains

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
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

func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	params := ListParams{
		Pagination: paginationParams,
	}

	return h.service.List(r.Context(), params)
}
