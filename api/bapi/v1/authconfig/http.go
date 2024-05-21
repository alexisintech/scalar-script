package authconfig

import (
	"net/http"

	"github.com/vgarvardt/gue/v2"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/database"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, gueClient *gue.Client) *HTTP {
	return &HTTP{service: NewService(db, gueClient)}
}

// PATCH /v1/beta_features/instance_settings
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Update(r.Context(), params)
}
