package domains

import (
	"encoding/json"
	"net/http"

	"clerk/api/apierror"
	sdkutils "clerk/pkg/sdk"
	"clerk/utils/clerk"

	"github.com/clerk/clerk-sdk-go/v2/domain"
	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service *Service
}

func NewHTTP(
	deps clerk.Deps,
	sdkConfigConstructor sdkutils.ConfigConstructor,
) *HTTP {
	return &HTTP{
		service: NewService(deps, sdkConfigConstructor),
	}
}

// GET /domains/{name}/exist
func (h *HTTP) Exists(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	name := chi.URLParam(r, "name")

	err := h.service.Exists(r.Context(), name)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// GET /instances/{instanceID}/domains
func (h *HTTP) List(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	return h.service.List(r.Context(), instanceID)
}

// POST /instances/{instanceID}/domains
func (h *HTTP) Create(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var createDomainParams domain.CreateParams
	err := json.NewDecoder(r.Body).Decode(&createDomainParams)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	return h.service.Create(r.Context(), instanceID, createDomainParams)
}

// PATCH /instances/{instanceID}/domains/{domainID}
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	var updateDomainParams domain.UpdateParams
	err := json.NewDecoder(r.Body).Decode(&updateDomainParams)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}

	instanceID := chi.URLParam(r, "instanceID")
	domainID := chi.URLParam(r, "domainID")
	return h.service.Update(r.Context(), instanceID, domainID, updateDomainParams)
}

// DELETE /instances/{instanceID}/domains/{domainID}
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	domainID := chi.URLParam(r, "domainID")
	return h.service.Delete(r.Context(), instanceID, domainID)
}

// GET /instances/{instanceID}/domains/{domainID}/status
func (h *HTTP) Status(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	domainID := chi.URLParam(r, "domainID")
	return h.service.Status(r.Context(), instanceID, domainID)
}

// POST /instances/{instanceID}/domains/{domainID}/status/dns/retry
func (h *HTTP) RetryDNS(
	w http.ResponseWriter,
	r *http.Request,
) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	domainID := chi.URLParam(r, "domainID")
	err := h.service.RetryDNS(r.Context(), instanceID, domainID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /instances/{instanceID}/domains/{domainID}/status/mail/retry
func (h *HTTP) RetryMail(
	w http.ResponseWriter,
	r *http.Request,
) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	domainID := chi.URLParam(r, "domainID")
	err := h.service.RetryMail(r.Context(), instanceID, domainID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /instances/{instanceID}/domains/{domainID}/status/ssl/retry
func (h *HTTP) RetrySSL(
	w http.ResponseWriter,
	r *http.Request,
) (interface{}, apierror.Error) {
	instanceID := chi.URLParam(r, "instanceID")
	domainID := chi.URLParam(r, "domainID")
	err := h.service.RetrySSL(r.Context(), instanceID, domainID)
	if err != nil {
		return nil, err
	}

	w.WriteHeader(http.StatusNoContent)
	return nil, nil
}

// POST /instances/{instanceID}/domains/{domainID}/verify_proxy
func (h *HTTP) VerifyProxy(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	var params VerifyProxyParams
	err := json.NewDecoder(r.Body).Decode(&params)
	if err != nil {
		return nil, apierror.InvalidRequestBody(err)
	}
	params.instanceID = chi.URLParam(r, "instanceID")
	params.domainID = chi.URLParam(r, "domainID")
	return h.service.VerifyProxy(r.Context(), params)
}
