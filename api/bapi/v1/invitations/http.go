package invitations

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	"clerk/utils/clerk"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	invitationsService *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		invitationsService: NewService(deps),
	}
}

// POST /v1/invitations
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.invitationsService.Create(r.Context(), params)
}

// GET /v1/invitations
func (h *HTTP) ReadAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := ReadAllParams{
		Statuses: r.URL.Query()["status"],
	}

	if r.URL.Query().Get(param.Paginated.Name) == "true" {
		return h.invitationsService.ReadAllPaginated(r.Context(), params)
	}

	if r.URL.Query().Get(param.Limit.Name) != "" || r.URL.Query().Get(param.Offset.Name) != "" {
		// Pagination was added long after this endpoint was released.
		// In order to avoid breaking existing users of the endpoint, we will only
		// consider pagination only if any of the pagination params, i.e.
		// limit or offset, is given. Otherwise, we'll assume that the
		// user is not using pagination and thus return all invitations.
		paginationParams, apiErr := pagination.NewFromRequest(r)
		if apiErr != nil {
			return nil, apiErr
		}
		params.Pagination = &paginationParams
	}

	return h.invitationsService.ReadAll(r.Context(), params)
}

// POST /v1/invitations/{invitationID}/revoke
func (h *HTTP) Revoke(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	invitationID := chi.URLParam(r, "invitationID")
	return h.invitationsService.Revoke(r.Context(), invitationID)
}
