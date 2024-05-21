package organization_memberships

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/clerkhttp"
	"clerk/repository"
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

// GET /v1/organizations/{organizationID}/memberships
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		OrganizationID:                          chi.URLParam(r, "organizationID"),
		OrganizationMembershipsFindAllModifiers: toReadAllMods(r),
		orderBy:                                 r.URL.Query().Get("order_by"),
	}

	return h.service.List(ctx, params, paginationParams)
}

func toReadAllMods(r *http.Request) repository.OrganizationMembershipsFindAllModifiers {
	var params repository.OrganizationMembershipsFindAllModifiers
	params.UserIDs = r.URL.Query()["user_id"]
	params.EmailAddresses = r.URL.Query()["email_address"]
	params.PhoneNumbers = r.URL.Query()["phone_number"]
	params.Usernames = r.URL.Query()["username"]
	params.Web3Wallets = r.URL.Query()["web3_wallet"]
	params.Query = r.URL.Query().Get("query")
	params.Roles = r.URL.Query()["role"]

	return params
}

// POST /v1/organizations/{organizationID}/memberships
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.OrganizationID = chi.URLParam(r, "organizationID")
	return h.service.Create(r.Context(), params)
}

// PATCH /v1/organizations/{organizationID}/memberships/{userID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.OrganizationID = chi.URLParam(r, "organizationID")
	params.UserID = chi.URLParam(r, "userID")
	return h.service.Update(r.Context(), params)
}

// PATCH /v1/organizations/{organizationID}/memberships/{userID}/metadata
func (h *HTTP) UpdateMetadata(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateMetadataParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.OrganizationID = chi.URLParam(r, "organizationID")
	params.UserID = chi.URLParam(r, "userID")
	return h.service.UpdateMetadata(r.Context(), params)
}

// DELETE /v1/organizations/{organizationID}/memberships/{userID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.Delete(r.Context(), chi.URLParam(r, "organizationID"), chi.URLParam(r, "userID"))
}
