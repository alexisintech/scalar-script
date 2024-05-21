package proxy_checks

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/bapi/v1/externalapp"
	"clerk/api/bapi/v1/internalapi"
	"clerk/pkg/clerkhttp"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

type HTTP struct {
	service *Service
}

func NewHTTP(
	clock clockwork.Clock,
	db database.Database,
	gueClient *gue.Client,
	externalAppClient *externalapp.Client,
	internalClient *internalapi.Client,
) *HTTP {
	return &HTTP{
		service: NewService(
			clock,
			db,
			gueClient,
			externalAppClient,
			internalClient,
		),
	}
}

func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := createParams{
		authorization: r.Header.Get("authorization"),
	}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	return h.service.Create(r.Context(), params)
}
