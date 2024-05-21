package applications

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/images"
	"clerk/pkg/billing"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/params"
	"clerk/utils/clerk"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service          *Service
	ownershipService *OwnershipService
}

func NewHTTP(deps clerk.Deps, svixClient *svix.Client, clerkImagesClient *clerkimages.Client, paymentProvider billing.PaymentProvider) *HTTP {
	return &HTTP{
		service:          NewService(deps, svixClient, clerkImagesClient, paymentProvider),
		ownershipService: NewOwnershipService(deps.DB()),
	}
}

// Middleware /applications/{applicationID}
func (h *HTTP) EnsureApplicationNotPendingDeletion(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	return r, h.service.EnsureApplicationNotPendingDeletion(r.Context(), chi.URLParam(r, "applicationID"))
}

// Middleware /applications/{applicationID}
func (h *HTTP) CheckApplicationOwner(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	err := h.ownershipService.AuthorizeUser(
		r.Context(),
		chi.URLParam(r, "applicationID"),
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Middleware
func (h *HTTP) CheckAdminIfOrganizationActive(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	return r, h.service.CheckAdminIfOrganizationActive(r.Context())
}

// POST /applications
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	applicationSettings, unmarshalErr := params.UnmarshalCreateApplicationSettings(r.Body)
	if unmarshalErr != nil {
		return nil, apierror.InvalidRequestBody(unmarshalErr)
	}

	return h.service.Create(ctx, applicationSettings)
}

// PATCH /applications/{applicationID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params params.UpdateApplicationSettings
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	applicationID := chi.URLParam(r, "applicationID")
	return h.service.Update(r.Context(), applicationID, params)
}

// POST /applications/{applicationID}/logo
func (h *HTTP) UpdateLogo(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	file, apiErr := images.ReadFileOrBase64(r)
	if apiErr != nil {
		return nil, apiErr
	}

	return h.service.UpdateLogo(r.Context(), updateImageParams{
		applicationID: chi.URLParam(r, "applicationID"),
		image:         file,
	})
}

// DELETE /applications/{applicationID}/logo
func (h *HTTP) DeleteLogo(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.DeleteLogo(r.Context(), chi.URLParam(r, "applicationID"))
}

// POST /applications/{applicationID}/favicon
func (h *HTTP) UpdateFavicon(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	file, apiErr := images.ReadFileOrBase64(r)
	if apiErr != nil {
		return nil, apiErr
	}

	return h.service.UpdateFavicon(r.Context(), updateImageParams{
		applicationID: chi.URLParam(r, "applicationID"),
		image:         file,
	})
}

// GET /applications
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	return h.service.List(r.Context())
}

// GET /applications/{applicationID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	applicationID := chi.URLParam(r, "applicationID")
	return h.service.Read(r.Context(), applicationID)
}

// DELETE /applications/{applicationID}
func (h *HTTP) Delete(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	applicationID := chi.URLParam(r, "applicationID")
	err := h.service.Delete(r.Context(), applicationID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /applications/{applicationID}/transfer_to_organization
func (h *HTTP) TransferToOrganization(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params MoveToOrganizationParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	applicationID := chi.URLParam(r, "applicationID")
	apiErr := h.service.TransferToOrganization(r.Context(), applicationID, params)
	if apiErr != nil {
		return nil, apiErr
	}
	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /applications/{applicationID}/transfer_to_user
func (h *HTTP) TransferToUser(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	applicationID := chi.URLParam(r, "applicationID")
	apiErr := h.service.TransferToUser(r.Context(), applicationID)
	if apiErr != nil {
		return nil, apiErr
	}
	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// GET /applications/{applicationID}/subscription_plans
func (h *HTTP) ListPlans(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	applicationID := chi.URLParam(r, "applicationID")
	return h.service.ListPlans(r.Context(), applicationID)
}

// GET /applications/{applicationID}/current_subscription
func (h *HTTP) CurrentSubscription(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	applicationID := chi.URLParam(r, "applicationID")
	return h.service.CurrentSubscription(r.Context(), applicationID)
}

// POST /applications/{applicationID}/refresh_payment_status
func (h *HTTP) RefreshPaymentStatus(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	appID := chi.URLParam(r, "applicationID")

	refreshPaymentStatusParams, unmarshalErr := params.UnmarshalRefreshPaymentStatusParams(r.Body)
	if unmarshalErr != nil {
		return nil, apierror.Unexpected(unmarshalErr)
	}

	return h.service.RefreshPaymentStatus(r.Context(), appID, refreshPaymentStatusParams)
}

// POST /applications/{applicationID}/products/{productID}
func (h *HTTP) SubscribeToProduct(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.SubscribeToProduct(r.Context(), subscribeToProductParams{
		applicationID: chi.URLParam(r, "applicationID"),
		productID:     chi.URLParam(r, "productID"),
	})
}

// DELETE /applications/{applicationID}/products/{productID}
func (h *HTTP) UnsubscribeFromProduct(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	applicationID := chi.URLParam(r, "applicationID")
	productID := chi.URLParam(r, "productID")
	err := h.service.UnsubscribeFromAddon(r.Context(), applicationID, productID)
	if err != nil {
		return nil, err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}
