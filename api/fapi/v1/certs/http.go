package certs

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/constants"
	"clerk/utils/database"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database) *HTTP {
	return &HTTP{
		service: NewService(db),
	}
}

// GET /v1/certificate-health
func (h *HTTP) Health(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	w.Header().Set("Content-Type", "text/plain")

	exists := h.service.CertificateHostExists(r.Context(), r.Header.Get(constants.XOriginalHost))

	var err error
	if exists {
		w.Header().Set("X-Clerk-Healthy", "1")
		_, err = w.Write([]byte("OK"))
	} else {
		w.WriteHeader(http.StatusNotFound)
		_, err = w.Write([]byte("Not Found"))
	}

	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return nil, nil
}
