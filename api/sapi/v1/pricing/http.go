package pricing

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/billing"
	"clerk/pkg/clerkhttp"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

type HTTP struct {
	service *Service
}

func NewHTTP(clock clockwork.Clock, db database.Database, paymentProvider billing.PaymentProvider) *HTTP {
	return &HTTP{
		service: NewService(clock, db, paymentProvider),
	}
}

func (h *HTTP) ListEnterprisePlans(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.ListEnterprisePlans(r.Context())
}

func (h *HTTP) CreateEnterprisePlan(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := CreateEnterprisePlanParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	return h.service.CreateEnterprisePlan(r.Context(), params)
}

func (h *HTTP) AssignToApplications(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := AssignToApplicationsParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}
	params.SubscriptionPlanID = chi.URLParam(r, "planID")
	return h.service.AssignToApplications(r.Context(), params)
}

func (h *HTTP) ListApplicationsWithTrials(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.ListApplicationsWithPendingTrials(r.Context())
}

func (h *HTTP) SetTrialForApplication(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	params := SetApplicationTrialParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.ApplicationID = chi.URLParam(r, "applicationID")
	return h.service.SetTrialForApplication(r.Context(), params)
}
