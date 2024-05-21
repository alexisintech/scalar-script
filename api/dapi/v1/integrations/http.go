package integrations

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/params"
	"clerk/pkg/vercel"
	"clerk/utils/clerk"

	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps, vercelClient *vercel.Client, jwksClient *jwks.Client) *HTTP {
	return &HTTP{
		service: NewService(deps, vercelClient, jwksClient),
	}
}

func (h *HTTP) CheckIntegrationOwner(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	integrationID := chi.URLParam(r, "integrationID")
	err := h.service.CheckIntegrationOwner(r.Context(), integrationID)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// UpsertVercel gets or creates a Vercel integration
// Note: this is a public endpoint
// POST /integrations
func (h *HTTP) UpsertVercel(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	// Parse params
	vercelIntegrationParams, err := params.UnmarshalVercelIntegrationParams(r.Body)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.UpsertVercel(ctx, &vercelIntegrationParams)
}

// UpsertByType currently used for Google Analytics
// Note: this endpoint is scoped to an instanceID
// PUT /instances/:instance_id/:integration_type
func (h *HTTP) UpsertByType(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	instanceID := chi.URLParam(r, "instanceID")
	integrationType := chi.URLParam(r, "integrationType")

	switch integrationType {
	case string(model.GoogleAnalyticsIntegration):
		// Parse params
		googleAnalyticsIntegrationParams, err := params.UnmarshalGoogleAnalyticsIntegrationParams(r.Body)
		if err != nil {
			return nil, apierror.InvalidRequestBody(err)
		}

		resp, serviceErr := h.service.ToggleGoogleAnalytics(ctx, instanceID, &googleAnalyticsIntegrationParams)

		if resp == nil && serviceErr == nil {
			w.WriteHeader(http.StatusNoContent)
			return nil, nil
		}

		return resp, serviceErr
	default:
		return nil, apierror.UnsupportedIntegrationType(integrationType)
	}
}

func (h *HTTP) GetUserInfo(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	integrationID := chi.URLParam(r, "integrationID")
	return h.service.GetUserInfo(r.Context(), integrationID)
}

func (h *HTTP) GetObjects(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	integrationID := chi.URLParam(r, "integrationID")
	return h.service.GetObjects(r.Context(), integrationID)
}

func (h *HTTP) GetObject(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	integrationID := chi.URLParam(r, "integrationID")
	objectID := chi.URLParam(r, "objectID")
	return h.service.GetObject(r.Context(), integrationID, objectID)
}

func (h *HTTP) ReadVercel(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	integrationID := chi.URLParam(r, "integrationID")
	return h.service.ReadVercel(r.Context(), integrationID)
}

func (h *HTTP) ReadByType(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	integrationType := chi.URLParam(r, "integrationType")
	return h.service.ReadByType(r.Context(), instanceID, model.IntegrationType(integrationType))
}

func (h *HTTP) Link(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	integrationID := chi.URLParam(r, "integrationID")

	// Parse params
	vercelLinkParams, err := params.UnmarshalVercelLinkParams(r.Body)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	return h.service.Link(r.Context(), integrationID, &vercelLinkParams)
}
