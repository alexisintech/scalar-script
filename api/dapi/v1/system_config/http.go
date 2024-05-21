package system_config

import (
	"net/http"

	"clerk/api/apierror"
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

// GET /system_config
func (h *HTTP) Read(_ http.ResponseWriter, _ *http.Request) (interface{}, apierror.Error) {
	return h.service.Read()
}
