package messaging

import (
	"errors"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/constants"
	"clerk/utils/clerk"
)

var (
	ErrInvalidTwilioSignature = errors.New("twilio: invalid signature")
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// POST /v1/events/twilio_sms_status
func (h *HTTP) TwilioSMSStatusCallback(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	if err := r.ParseForm(); err != nil {
		return nil, apierror.Unexpected(err)
	}

	params := make(map[string]string)

	for key, values := range r.PostForm {
		params[key] = values[0]
	}

	signature := r.Header.Get(constants.TwilioSignatureHeader)
	traceIDEncoded := r.URL.Query().Get(constants.TraceIDQueryParam)

	return nil, h.service.TwilioSMSStatusCallback(ctx, params, signature, traceIDEncoded)
}
