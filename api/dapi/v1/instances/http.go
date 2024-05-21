package instances

import (
	"encoding/json"
	"net/http"
	"strings"

	"clerk/api/apierror"
	"clerk/api/dapi/v1/domains"
	"clerk/pkg/externalapis/clerkimages"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/params"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/clerk"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/domain"
	"github.com/clerk/clerk-sdk-go/v2/instancesettings"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	domainsService           *domains.Service
	service                  *Service
	instanceOwnershipService *OwnershipService
}

func NewHTTP(deps clerk.Deps, svixClient *svix.Client, clerkImagesClient *clerkimages.Client, sdkConfigConstructor sdkutils.ConfigConstructor) *HTTP {
	return &HTTP{
		service:                  NewService(deps, svixClient, clerkImagesClient, sdkConfigConstructor),
		instanceOwnershipService: NewOwnershipService(deps.DB()),
		domainsService:           domains.NewService(deps, sdkConfigConstructor),
	}
}

// POST /applications/{applicationID}/production_instance
func (h *HTTP) CreateProduction(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	productionInstanceSettings, unmarshalErr := params.UnmarshalProductionInstanceSettings(r.Body)
	if unmarshalErr != nil {
		return nil, apierror.InvalidRequestBody(unmarshalErr)
	}

	applicationID := chi.URLParam(r, "applicationID")
	return h.service.CreateProduction(ctx, applicationID, &productionInstanceSettings)
}

// POST /applications/{applicationID}/validate_cloning
func (h *HTTP) ValidateCloning(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params ValidateCloningParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	params.ApplicationID = chi.URLParam(r, "applicationID")
	apiErr := h.service.ValidateCloning(r.Context(), params)
	if apiErr != nil {
		return nil, apiErr
	}
	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// Middleware /instances/{instanceID}
func (h *HTTP) EnsureApplicationNotPendingDeletion(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	err := h.service.EnsureApplicationNotPendingDeletion(r.Context(), instanceID)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// Middleware /instances/{instanceID}
func (h *HTTP) CheckInstanceOwner(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	err := h.instanceOwnershipService.CheckInstanceOwner(r.Context(), instanceID)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// GET /applications/{applicationID}/instances
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	applicationID := chi.URLParam(r, "applicationID")
	return h.service.List(r.Context(), applicationID)
}

// GET /instances/{instanceID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Read(r.Context(), instanceID)
}

// DELETE /instances/{instanceID}
func (h *HTTP) Delete(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	err := h.service.Delete(r.Context(), instanceID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// GET /instances/{instanceID}/deploy_status
func (h *HTTP) DeployStatus(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	return h.service.DeployStatus(r.Context(), chi.URLParam(r, "instanceID"))
}

// POST /instances/{instanceID}/status/mail/retry
func (h *HTTP) RetryMail(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	err := h.service.RetryMail(r.Context(), instanceID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /instances/{instanceID}/status/ssl/retry
func (h *HTTP) RetrySSL(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	err := h.service.RetrySSL(r.Context(), instanceID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// PATCH /instances/{instanceID}
func (h *HTTP) UpdateSettings(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var updateParams instancesettings.UpdateParams
	err := json.NewDecoder(r.Body).Decode(&updateParams)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	apiErr := h.service.Update(r.Context(), instanceID, updateParams)
	if apiErr != nil {
		return nil, apiErr
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

type updateCommunicationParams struct {
	BlockedCountryCodes *[]string `json:"blocked_country_codes" form:"blocked_country_codes"`
}

// PATCH /instances/{instanceID}/communication
func (h *HTTP) UpdateCommunication(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params updateCommunicationParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	apiErr := h.service.UpdateCommunication(r.Context(), params)
	if apiErr != nil {
		return nil, apiErr
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /instances/{instanceID}/change_domain
func (h *HTTP) UpdateHomeURL(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	type updateHomeURLParams struct {
		HomeURL string `json:"home_url"`
	}
	var params updateHomeURLParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	// HomeUrl comes as a url. We need to pass an FQDN.
	updateDomainParams := domain.UpdateParams{
		Name: sdk.String(strings.TrimPrefix(params.HomeURL, "https://")),
	}

	instanceID := chi.URLParam(r, "instanceID")
	_, apiErr := h.domainsService.UpdatePrimaryDomain(
		r.Context(),
		instanceID,
		updateDomainParams,
	)
	if apiErr != nil {
		return nil, apiErr
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /instances/{instanceID}/patch_me_password
func (h *HTTP) UpdatePatchMePassword(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var updatePatchMePasswordParams updatePatchMePasswordParams
	err := json.NewDecoder(r.Body).Decode(&updatePatchMePasswordParams)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	apiErr := h.service.UpdatePatchMePassword(r.Context(), instanceID, updatePatchMePasswordParams)
	if apiErr != nil {
		return nil, apiErr
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// PUT /instances/{instanceID}/api_versions
func (h *HTTP) UpdateAPIVersion(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var params updateAPIVersionParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	updateError := h.service.UpdateAPIVersion(r.Context(), chi.URLParam(r, "instanceID"), params)
	if updateError != nil {
		return nil, updateError
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// GET /instances/{instanceID}/api_versions
func (h *HTTP) GetAvailableAPIVersions(_ http.ResponseWriter, _ *http.Request) (interface{}, apierror.Error) {
	return h.service.GetAvailableAPIVersions(), nil
}
