package redirect_urls

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	"github.com/clerk/clerk-sdk-go/v2/redirecturl"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, sdkConfigConstructor sdkutils.ConfigConstructor) *HTTP {
	return &HTTP{
		service: NewService(db, sdkConfigConstructor),
	}
}

// POST /instances/{instanceID}/redirect_urls
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params redirecturl.CreateParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Create(r.Context(), instanceID, params)
}

// GET /instances/{instanceID}/redirect_urls
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.List(r.Context(), instanceID)
}

// DELETE /instances/{instanceID}/redirect_urls/{urlID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	urlID := chi.URLParam(r, "urlID")
	return h.service.Delete(r.Context(), instanceID, urlID)
}
