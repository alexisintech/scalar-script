package smscountrytiers

import (
	"clerk/api/apierror"
	"clerk/utils/clerk"
	"net/http"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// GET /internal/sms_country_tiers
// This endpoint is cached in Cloudflare using a cache rule
func (h *HTTP) GetCountryTiers(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	tiers, err := h.service.GetCountryTiers(ctx)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return tiers, nil
}
