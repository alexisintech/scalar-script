package instance_keys

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

// GET /instance_keys
func (h *HTTP) ListAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.ListAll(r.Context())
}

// GET /instances/{instanceID}/instance_keys
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.List(r.Context(), instanceID)
}

// GET /instances/{instanceID}/instance_keys/{keyID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	instanceKeyID := chi.URLParam(r, "instanceKeyID")
	return h.service.Read(r.Context(), instanceID, instanceKeyID)
}

// POST /instances/{instanceID}/instance_keys
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	var inputKey InstanceKey
	if err := json.NewDecoder(r.Body).Decode(&inputKey); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Create(ctx, &inputKey)
}

// DELETE /instances/{instanceID}/instance_keys/{keyID}
func (h *HTTP) Delete(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	instanceKeyID := chi.URLParam(r, "instanceKeyID")

	err := h.service.Delete(r.Context(), instanceID, instanceKeyID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}
