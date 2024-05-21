package users

import (
	"net/http"
	"unicode/utf8"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/api/shared/serializable"
	"clerk/api/shared/users"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/uploads"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

// HTTP is the http layer for all requests related to users in server API.
// Its responsibility is to extract any relevant information required by the service layer from the incoming request.
// It's also responsible for verifying the correctness of the incoming payload.
type HTTP struct {
	db    database.Database
	clock clockwork.Clock

	listService          *ListService
	serializableService  *serializable.Service
	service              *Service
	shUsersService       *users.Service
	proxyImageURLService *ProxyImageURLService
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		db:                   deps.DB(),
		clock:                deps.Clock(),
		listService:          NewListService(deps.Clock(), deps.ReadOnlyDB()),
		serializableService:  serializable.NewService(deps.Clock()),
		service:              NewService(deps),
		shUsersService:       users.NewService(deps),
		proxyImageURLService: NewProxyImageURLService(deps.ReadOnlyDB()),
	}
}

// Middleware /v1/users/{userID}
func (h *HTTP) CheckUserInInstance(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	err := h.service.CheckUserInInstance(r.Context(), userID)
	return r, err
}

// GET /v1/users
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := toReadAllParams(r)

	if params.query != "" && utf8.RuneCountInString(params.query) < 3 {
		return nil, apierror.FormParameterMinLengthExceeded("search", 3)
	}

	params.orderBy = r.URL.Query().Get("order_by")

	pagination, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	return h.listService.ReadAll(r.Context(), params, pagination)
}

// GET /v1/users/count
func (h *HTTP) Count(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.CountAll(r.Context(), toReadAllParams(r))
}

func toReadAllParams(r *http.Request) readAllParams {
	return readAllParams{
		userIDs:           r.URL.Query()["user_id"],
		externalIDs:       r.URL.Query()["external_id"],
		organizationIDs:   r.URL.Query()["organization_id"],
		emailAddresses:    r.URL.Query()["email_address"],
		phoneNumbers:      r.URL.Query()["phone_number"],
		usernames:         r.URL.Query()["username"],
		web3Wallets:       r.URL.Query()["web3_wallet"],
		lastActiveAtSince: r.URL.Query()["last_active_at_since"],
		query:             r.URL.Query().Get("query"),
	}
}

// POST /v1/users
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.Create(r.Context(), params)
}

// GET /v1/users/{userID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	return h.service.Read(r.Context(), userID)
}

// DELETE /v1/users/{userID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	ctx := r.Context()
	env := environment.FromContext(ctx)
	return h.shUsersService.Delete(ctx, env, userID)
}

// PATCH /v1/users/{userID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	userID := chi.URLParam(r, "userID")
	return h.service.Update(r.Context(), userID, params)
}

// UpdateMetadata handles requests to
// PATCH /v1/users/{userID}/metadata
func (h *HTTP) UpdateMetadata(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateMetadataParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	return h.service.UpdateMetadata(r.Context(), chi.URLParam(r, "userID"), params)
}

// UpdateProfileImage
// POST /v1/users/{userID}/profile_image
func (h *HTTP) UpdateProfileImage(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")

	filePart, err := uploads.ReadOneFile(w, r)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	if filePart == nil {
		return nil, apierror.RequestWithoutImage()
	}

	return h.service.UpdateProfileImage(r.Context(), userID, filePart)
}

// DELETE /v1/users/{userID}/profile_image
func (h *HTTP) DeleteProfileImage(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	return h.service.DeleteProfileImage(r.Context(), userID)
}

// GET /v1/users/{userID}/oauth_access_tokens/{provider}
func (h *HTTP) ListOAuthAccessTokens(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	providerID := chi.URLParam(r, "provider")

	if r.URL.Query().Get(param.Paginated.Name) == "true" {
		return h.service.ListOAuthAccessTokensPaginated(r.Context(), userID, providerID)
	}
	return h.service.ListOAuthAccessTokens(r.Context(), userID, providerID)
}

// POST /v1/users/{userID}/verify_password
func (h *HTTP) VerifyPassword(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")

	params := VerifyPasswordParams{}
	err := clerkhttp.Decode(r, &params)
	if err != nil {
		return nil, err
	}

	err = h.service.VerifyPassword(r.Context(), userID, params.Password)
	if err != nil {
		return nil, err
	}

	return struct {
		Verified bool `json:"verified"`
	}{true}, nil
}

// POST /v1/users/{userID}/verify_totp
func (h *HTTP) VerifyTOTP(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")

	params := VerifyTOTPParams{}
	err := clerkhttp.Decode(r, &params)
	if err != nil {
		return nil, err
	}

	codeType, err := h.service.VerifyTOTP(r.Context(), userID, params.Code)
	if err != nil {
		return nil, err
	}

	return struct {
		Verified bool   `json:"verified"`
		CodeType string `json:"code_type"`
	}{true, codeType}, nil
}

// DELETE /v1/users/{userID}/mfa
func (h *HTTP) DisableMFA(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")

	err := h.service.DisableMFA(r.Context(), userID)
	if err != nil {
		return nil, err
	}

	return struct {
		UserID string `json:"user_id"`
	}{userID}, nil
}

// POST /v1/users/{userID}/ban
func (h *HTTP) Ban(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	return h.service.Ban(r.Context(), userID)
}

// POST /v1/users/{userID}/unban
func (h *HTTP) Unban(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	return h.service.Unban(r.Context(), userID)
}

// POST /v1/users/{userID}/lock
func (h *HTTP) Lock(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	return h.service.Lock(r.Context(), userID)
}

// POST /v1/users/{userID}/unlock
func (h *HTTP) Unlock(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")
	return h.service.Unlock(r.Context(), userID)
}

// GET /v1/users/{userID}/organization_memberships
func (h *HTTP) ListOrganizationMemberships(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}
	return h.service.ListOrganizationMemberships(r.Context(), ListOrganizationMembershipsParams{
		UserID: chi.URLParam(r, "userID"),
	}, paginationParams)
}

// GET /v1/internal/proxy_image_url
func (h *HTTP) ProxyImageURL(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	imageURL := r.URL.Query().Get(param.ImageURL.Name)
	return h.proxyImageURLService.ProxyImageURL(r.Context(), ProxyImageURLParams{
		ImageURL: imageURL,
	})
}
