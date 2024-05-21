package organizations

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/params"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/clerk"

	"github.com/clerk/clerk-sdk-go/v2/organization"
	"github.com/clerk/clerk-sdk-go/v2/organizationmembership"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps, newSDKConfig sdkutils.ConfigConstructor, paymentProvider clerkbilling.PaymentProvider) *HTTP {
	return &HTTP{
		service: NewService(deps, newSDKConfig, paymentProvider),
	}
}

// GET /instances/{instanceID}/organizations
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListParams{
		InstanceID: chi.URLParam(r, "instanceID"),
		Query:      r.URL.Query().Get("query"),
		UserIDs:    r.URL.Query()["user_id"],
		OrderBy:    clerkhttp.GetOptionalQueryParam(r, "order_by"),
	}
	return h.service.List(r.Context(), params, paginationParams)
}

// POST /instances/{instanceID}/organizations
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateOrganizationParams{}
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	params.InstanceID = chi.URLParam(r, "instanceID")
	return h.service.Create(r.Context(), params)
}

// GET /instances/{instanceID}/organizations/{organizationIDorSlug}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	organizationIDorSlug := chi.URLParam(r, "organizationIDorSlug")

	return h.service.Read(r.Context(), instanceID, organizationIDorSlug)
}

// PATCH /instances/{instanceID}/organizations/{organizationID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	organizationID := chi.URLParam(r, "organizationID")

	params := &updateParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Update(r.Context(), params, organizationID, instanceID)
}

// DELETE /instances/{instanceID}/organizations/{organizationID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	organizationID := chi.URLParam(r, "organizationID")

	return h.service.Delete(r.Context(), organizationID, instanceID)
}

// PATCH /instances/{instanceID}/organizations/{organizationID}/metadata
func (h *HTTP) UpdateMetadata(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	organizationID := chi.URLParam(r, "organizationID")

	params := &organization.UpdateMetadataParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.UpdateMetadata(r.Context(), organizationID, instanceID, *params)
}

func (h *HTTP) DeleteLogo(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.DeleteLogo(r.Context(), deleteLogoParams{
		organizationID: chi.URLParam(r, "organizationID"),
		instanceID:     chi.URLParam(r, "instanceID"),
	})
}

// PATCH /instances/{instanceID}/organizations/{organizationID}/logo
func (h *HTTP) UpdateLogo(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	// Allow up to 10MB files
	const tenMB = 10 * 1024 * 1024
	if err := r.ParseMultipartForm(tenMB); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	if file == nil {
		return nil, apierror.RequestWithoutImage()
	}

	return h.service.UpdateLogo(r.Context(), updateLogoParams{
		organizationID: chi.URLParam(r, "organizationID"),
		instanceID:     chi.URLParam(r, "instanceID"),
		file:           file,
		filename:       header.Filename,
	})
}

// Middleware
func (h *HTTP) CheckOrganizationsEnabled(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	return r, h.service.CheckOrganizationsEnabled(r.Context(), chi.URLParam(r, "instanceID"))
}

// Middleware
func (h *HTTP) CheckOrganizationAdmin(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	return r, h.service.CheckOrganizationAdmin(r.Context(), chi.URLParam(r, "organizationID"))
}

// POST /organizations/{organizationID}/refresh_payment_status
func (h *HTTP) RefreshPaymentStatus(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	organizationID := chi.URLParam(r, "organizationID")
	refreshPaymentStatusParams, unmarshalErr := params.UnmarshalRefreshPaymentStatusParams(r.Body)
	if unmarshalErr != nil {
		return nil, apierror.Unexpected(unmarshalErr)
	}

	return h.service.RefreshPaymentStatus(r.Context(), organizationID, refreshPaymentStatusParams)
}

// GET /organizations/{organizationID}/subscription
func (h *HTTP) Subscription(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	organizationID := chi.URLParam(r, "organizationID")
	return h.service.ReadSubscription(r.Context(), organizationID)
}

// GET /organizations/{organizationID}/subscription_plans
func (h *HTTP) ListPlans(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	organizationID := chi.URLParam(r, "organizationID")
	return h.service.ListPlans(r.Context(), organizationID)
}

// GET /organizations/{organizationID}/current_subscription
func (h *HTTP) CurrentSubscription(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	organizationID := chi.URLParam(r, "organizationID")
	return h.service.CurrentSubscription(r.Context(), organizationID)
}

// GET /organization/{organizationID}/memberships
func (h *HTTP) ListMemberships(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	return h.service.ListMemberships(r.Context(), instanceID, ListMembershipsParams{
		Query:          clerkhttp.GetOptionalQueryParam(r, "query"),
		Roles:          r.URL.Query()["role"],
		OrganizationID: chi.URLParam(r, "organizationID"),
		OrderBy:        clerkhttp.GetOptionalQueryParam(r, "order_by"),
	}, paginationParams)
}

// POST /organization/{organizationID}/memberships
func (h *HTTP) CreateMembership(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := CreateMembershipParams{}
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	params.InstanceID = chi.URLParam(r, "instanceID")
	params.OrganizationID = chi.URLParam(r, "organizationID")

	return h.service.CreateMembership(r.Context(), params)
}

// PATCH /organization/{organizationID}/memberships/{userID}
func (h *HTTP) UpdateMembership(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := organizationmembership.UpdateParams{}
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	params.UserID = chi.URLParam(r, "userID")
	params.OrganizationID = chi.URLParam(r, "organizationID")

	return h.service.UpdateMembership(r.Context(), chi.URLParam(r, "instanceID"), &params)
}

// DELETE /organization/{organizationID}/memberships/{userID}
func (h *HTTP) DeleteMemebership(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := DeleteMembershipParams{}
	params.UserID = chi.URLParam(r, "userID")
	params.OrganizationID = chi.URLParam(r, "organizationID")
	params.InstanceID = chi.URLParam(r, "instanceID")

	return h.service.DeleteMembership(r.Context(), params)
}
