package applications

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

func (h *HTTP) GetApplications(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	params := ListApplicationsParams{
		Pagination: paginationParams,
		Query:      r.URL.Query().Get("query"),
	}

	return h.service.ListApplications(r.Context(), params)
}

func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	appID := chi.URLParam(r, "applicationID")
	return h.service.Read(r.Context(), appID)
}

func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	params.ApplicationID = chi.URLParam(r, "applicationID")

	return h.service.Update(r.Context(), params)
}
