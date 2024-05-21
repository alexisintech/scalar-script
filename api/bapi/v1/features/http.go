package features

import (
	"net/http"

	"clerk/api/apierror"
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

func (h *HTTP) CheckSupportedByPlan(billingFeature string) func(http.ResponseWriter, *http.Request) (*http.Request, apierror.Error) {
	return func(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
		err := h.service.CheckSupportedByPlan(r.Context(), billingFeature)
		return r, err
	}
}
