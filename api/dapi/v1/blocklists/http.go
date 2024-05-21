package blocklists

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/database"

	"github.com/clerk/clerk-sdk-go/v2/blocklistidentifier"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, newSDKConfig sdkutils.ConfigConstructor) *HTTP {
	return &HTTP{
		service: NewService(db, newSDKConfig),
	}
}

// POST /instances/{instanceID}/blocklist_identifiers
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params blocklistidentifier.CreateParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Create(r.Context(), instanceID, params)
}

// DELETE /instances/{instanceID}/blocklist_identifiers/{identifierID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	identifierID := chi.URLParam(r, "identifierID")
	return h.service.Delete(r.Context(), instanceID, identifierID)
}

// GET /instances/{instanceID}/blocklist_identifiers
func (h *HTTP) ListAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.ListAll(r.Context(), instanceID)
}
