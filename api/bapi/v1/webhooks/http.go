package webhooks

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/externalapis/svix"
	"clerk/utils/database"
)

// HTTP is the http layer for all requests related to webhooks in server API.
// Its responsibility is to verify the correctness of the incoming payload and
// extract any relevant information required by the service layer from the incoming request.
type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, svixClient *svix.Client) *HTTP {
	return &HTTP{
		service: NewService(db, svixClient),
	}
}

// POST /v1/webhooks/svix
func (h *HTTP) CreateSvix(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.CreateSvix(r.Context())
}

// DELETE /v1/webhooks/svix
func (h *HTTP) DeleteSvix(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	err := h.service.DeleteSvix(r.Context())
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /v1/webhooks/svix_url
func (h *HTTP) CreateSvixURL(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.CreateSvixURL(r.Context())
}
