package users

import (
	"encoding/json"
	"io"
	"net/http"

	"clerk/api/apierror"
	shorigin "clerk/api/shared/origin"
	"clerk/api/shared/pagination"
	sdkutils "clerk/pkg/sdk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/form"
	"clerk/utils/param"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/user"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	db      database.Database
	service *Service

	// repositories
	authConfigRepo *repository.AuthConfig
	instanceRepo   *repository.Instances
}

func NewHTTP(
	deps clerk.Deps,
	dapiSDKClientConfig *sdk.ClientConfig,
	newSDKConfig sdkutils.ConfigConstructor,
) *HTTP {
	return &HTTP{
		db:             deps.DB(),
		service:        NewService(deps, dapiSDKClientConfig, newSDKConfig),
		authConfigRepo: repository.NewAuthConfig(),
		instanceRepo:   repository.NewInstances(),
	}
}

// Middleware /instances/{instanceID}/users/{userID}
func (h *HTTP) CheckUserInInstance(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	userID := chi.URLParam(r, "userID")
	err := h.service.CheckUserInInstance(r.Context(), instanceID, userID)
	return r, err
}

// POST /instances/{instanceID}/users
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params user.CreateParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Create(r.Context(), instanceID, params)
}

// GET /instances/{instanceID}/users
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	// It's Dashboard, we don't need to reject unknown params
	params := listParams{
		orderBy:         r.FormValue(param.OrderBy.Name),
		query:           r.FormValue(param.Query.Name),
		organizationIDs: form.GetStringArray(r.Form, "organization_id"),
	}

	pagination, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	instanceID := chi.URLParam(r, "instanceID")

	return h.service.List(r.Context(), instanceID, params, pagination)
}

// GET /instances/{instanceID}/users/count
func (h *HTTP) Count(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := listParams{
		query: r.FormValue(param.Query.Name),
	}

	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Count(r.Context(), instanceID, params)
}

// GET /instances/{instanceID}/users/{userID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	userID := chi.URLParam(r, "userID")
	return h.service.Read(r.Context(), instanceID, userID)
}

// DELETE /instances/{instanceID}/users/{userID}
func (h *HTTP) Delete(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	userID := chi.URLParam(r, "userID")
	err := h.service.Delete(r.Context(), instanceID, userID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// PATCH /instances/{instanceID}/users/{userID}
func (h *HTTP) Update(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")

	params := &updateParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	apiErr := h.service.Update(r.Context(), params, userID)
	if apiErr != nil {
		return nil, apiErr
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// PUT /preferences
func (h *HTTP) SetPreferences(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params struct {
		Preferences string `json:"preferences"`
	}

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if len(reqBody) == 0 {
		return nil, apierror.FormMissingParameter("preferences")
	}

	err = json.Unmarshal(reqBody, &params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	isValidJSON := json.Unmarshal([]byte(params.Preferences), &json.RawMessage{}) == nil
	if !isValidJSON {
		return nil, apierror.FormInvalidTypeParameter(
			"preferences", "string containing a JSON object")
	}

	apierr := h.service.SetPreferences(r.Context(), params.Preferences)
	if apierr != nil {
		return nil, apierr
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /instances/{instanceID}/users/{userID}/impersonate
func (h *HTTP) Impersonate(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	userID := chi.URLParam(r, "userID")

	origin := r.Header.Get("Origin")
	if !shorigin.ValidateDashboardOrigin(origin) {
		return nil, apierror.InvalidOriginHeader()
	}

	redirectURL, err := h.service.ImpersonateURL(r.Context(), ImpersonateURLParams{
		UserID: userID,
		Host:   origin,
	})
	if err != nil {
		return nil, err
	}

	return redirectURL, nil
}

// POST /instances/{instanceID}/users/{userID}/ban
func (h *HTTP) Ban(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	userID := chi.URLParam(r, "userID")
	return h.service.Ban(r.Context(), instanceID, userID)
}

// POST /instances/{instanceID}/users/{userID}/unban
func (h *HTTP) Unban(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	userID := chi.URLParam(r, "userID")
	return h.service.Unban(r.Context(), instanceID, userID)
}

// POST /instances/{instanceID}/users/{userID}/unlock
func (h *HTTP) Unlock(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	userID := chi.URLParam(r, "userID")
	return h.service.Unlock(r.Context(), instanceID, userID)
}

// GET /instances/{instanceID}/users/{userID}/organization_memberships
func (h *HTTP) ListOrganizationMemberships(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	pagination, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListOrganizationMembershipsParams{InstanceID: chi.URLParam(r, "instanceID"), UserID: chi.URLParam(r, "userID"), Pagination: pagination}

	return h.service.ListOrganizationMemberships(r.Context(), params)
}
