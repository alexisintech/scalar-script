package comms

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"
)

// HTTP is the http layer for all requests related to communications in server API.
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

// POST /v1/sms_messages
func (h *HTTP) CreateSMS(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateSMSParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.CreateSMS(r.Context(), params)
}

// POST /v1/emails
func (h *HTTP) CreateEmail(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateEmailParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.CreateEmail(r.Context(), params)
}
