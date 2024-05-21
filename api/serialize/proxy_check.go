package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const ObjectProxyCheck = "proxy_check"

type ProxyCheckResponse struct {
	Object     string `json:"object"`
	ID         string `json:"id"`
	DomainID   string `json:"domain_id"`
	ProxyURL   string `json:"proxy_url"`
	Successful bool   `json:"successful"`
	LastRunAt  *int64 `json:"last_run_at"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

func ProxyCheck(proxyCheck *model.ProxyCheck) *ProxyCheckResponse {
	res := &ProxyCheckResponse{
		Object:     ObjectProxyCheck,
		ID:         proxyCheck.ID,
		DomainID:   proxyCheck.DomainID,
		ProxyURL:   proxyCheck.ProxyURL,
		Successful: proxyCheck.Successful,
		CreatedAt:  time.UnixMilli(proxyCheck.CreatedAt),
		UpdatedAt:  time.UnixMilli(proxyCheck.UpdatedAt),
	}
	if proxyCheck.LastRunAt.Valid {
		timestamp := proxyCheck.LastRunAt.Time.UTC().UnixMilli()
		res.LastRunAt = &timestamp
	}
	return res
}
