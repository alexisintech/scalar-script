package billing

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/billing"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/environment"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
	"github.com/vgarvardt/gue/v2"
)

type HTTP struct {
	service *Service
}

func NewHTTP(db database.Database, gueClient *gue.Client, billingConnector billing.Connector) *HTTP {
	return &HTTP{
		service: NewService(db, gueClient, billingConnector),
	}
}

// POST /instances/{instanceID}/billing/connect
func (h *HTTP) Connect(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := &connectParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	return h.service.Connect(r.Context(), params)
}

// GET /billing/connect_oauth_callback
func (h *HTTP) ConnectCallback(w http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	query := r.URL.Query()
	redirectURL, apiErr := h.service.ConnectCallback(r.Context(), connectCallbackParams{
		code:  query.Get("code"),
		nonce: query.Get("state"),
	})
	if apiErr != nil {
		return nil, apiErr
	}

	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
	return nil, nil
}

func (h *HTTP) EnsureInstanceHasConnectedBillingAccount(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	env := environment.FromContext(r.Context())
	if !env.Instance.ExternalBillingAccountID.Valid {
		return r, apierror.NoBillingAccountConnectedToInstance()
	}
	return r, nil
}

// POST /instances/{instanceID}/billing/plans
func (h *HTTP) CreatePlan(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := &CreatePlanParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	return h.service.CreatePlan(r.Context(), params)
}

// GET /instances/{instanceID}/billing/plans
func (h *HTTP) GetPlans(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := GetPlansParams{
		CustomerType: r.URL.Query().Get("customer_type"),
		Query:        clerkhttp.GetOptionalQueryParam(r, "query"),
	}
	return h.service.GetPlans(r.Context(), params)
}

// DELETE /instances/{instanceID}/billing/plans/{planID}
func (h *HTTP) DeletePlan(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	planID := chi.URLParam(r, "planID")
	return h.service.DeletePlan(r.Context(), planID)
}

// PATCH /instances/{instanceID}/billing/plans/{planID}
func (h *HTTP) UpdatePlan(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := &UpdatePlanParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	planID := chi.URLParam(r, "planID")
	return h.service.UpdatePlan(r.Context(), planID, params)
}

// PATCH /instances/{instanceID}/billing
func (h *HTTP) Config(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := &UpdateConfigParams{}
	if err := json.NewDecoder(r.Body).Decode(params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.UpdateConfig(r.Context(), instanceID, params)
}
