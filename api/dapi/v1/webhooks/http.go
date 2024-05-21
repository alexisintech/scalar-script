package webhooks

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/externalapis/svix"
	"clerk/utils/database"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, svixClient *svix.Client) *HTTP {
	return &HTTP{
		service: NewService(db, svixClient),
	}
}

// POST /instances/{instanceID}/webhooks/svix
func (h *HTTP) CreateSvix(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.CreateSvix(r.Context())
}

// GET /instances/{instanceID}/webhooks/svix
func (h *HTTP) GetSvixStatus(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.GetSvixStatus(r.Context())
}

// DELETE /instances/{instanceID}/webhooks/svix
func (h *HTTP) DeleteSvix(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := h.service.DeleteSvix(r.Context())
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}
