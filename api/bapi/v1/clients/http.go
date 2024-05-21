package clients

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

// HTTP is the http layer for all requests related to clients in server API.
// Its responsibility is to verify the correctness of the incoming payload and
// extract any relevant information required by the service layer from the incoming request.
type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// GET /v1/clients
func (h *HTTP) ReadAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	pagination, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	if r.URL.Query().Get(param.Paginated.Name) == "true" {
		return h.service.ReadAllPaginated(r.Context(), pagination)
	}

	return h.service.ReadAll(r.Context(), pagination)
}

// GET /v1/clients/{clientID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	clientID := chi.URLParam(r, "clientID")
	return h.service.Read(r.Context(), clientID)
}

// POST /v1/clients/verify
func (h *HTTP) Verify(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := VerifyParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Verify(r.Context(), params)
}
