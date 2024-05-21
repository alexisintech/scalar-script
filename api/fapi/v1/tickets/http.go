package tickets

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/utils/clerk"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// GET /v1/tickets/accept
func (h *HTTP) Accept(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ticket := r.URL.Query().Get("ticket")
	afterImpersonateRedirectURL := r.URL.Query().Get("redirect_url")

	redirectURL, err := h.service.BuildTicketRedirectURL(r.Context(), ticket, afterImpersonateRedirectURL)
	if err != nil {
		return nil, err
	}

	http.Redirect(w, r, redirectURL.String(), http.StatusSeeOther)
	return nil, nil
}
