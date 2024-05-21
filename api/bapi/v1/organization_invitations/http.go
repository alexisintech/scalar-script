package organization_invitations

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/constants"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// POST /v1/organizations/{organizationID}/invitations
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), chi.URLParam(r, "organizationID"), params)
}

// POST /v1/organizations/{organizationID}/invitations/bulk
func (h *HTTP) CreateBulk(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	var params []CreateParams
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	return h.service.CreateBulk(r.Context(), chi.URLParam(r, "organizationID"), params)
}

// GET /v1/organizations/{organizationID}/invitations
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		OrganizationID: chi.URLParam(r, "organizationID"),
		Statuses:       r.URL.Query()["status"],
	}
	return h.service.List(r.Context(), params, paginationParams)
}

// Deprecated: Use 'List' instead
// GET /v1/organizations/{organizationID}/invitations/pending
func (h *HTTP) ListPending(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		OrganizationID: chi.URLParam(r, "organizationID"),
		Statuses:       []string{constants.StatusPending},
	}
	return h.service.List(r.Context(), params, paginationParams)
}

// GET /v1/organizations/{organizationID}/invitations/{invitationID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Read(r.Context(), chi.URLParam(r, "organizationID"), chi.URLParam(r, "invitationID"))
}

// POST /v1/organizations/{organizationID}/invitations/{invitationID}/revoke
func (h *HTTP) Revoke(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := RevokeParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	params.OrganizationID = chi.URLParam(r, "organizationID")
	params.InvitationID = chi.URLParam(r, "invitationID")

	return h.service.Revoke(r.Context(), params)
}
