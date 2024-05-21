package tokens

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/model"
	"clerk/pkg/cenv"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/sampling"
	"clerk/utils/clerk"
	"clerk/utils/form"
	"clerk/utils/log"
	"clerk/utils/param"

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

// POST /v1/me/tokens
func (h *HTTP) CreateForJWTService(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	err := form.Check(r.Form, param.NewList(param.NewSet(param.Service), param.NewSet()))
	if err != nil {
		return nil, err
	}
	user := requesting_user.FromContext(ctx)
	serviceType := *form.GetString(r.Form, param.Service.Name)

	return h.service.CreateForJWTService(ctx, user, model.ToJWTServiceType(serviceType))
}

// POST /v1/client/sessions/{sessionID}/tokens
func (h *HTTP) CreateSessionToken(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sessionID")

	// only log a sample of these requests, given the sheer amount of them
	// and the fact that they don't contain any useful information
	if logLine, logLineExists := log.GetLogLine(ctx); logLineExists {
		logLine.IsIncludedInLogs = sampling.IsIncluded(cenv.GetFloat64(cenv.ClerkSessionTokenLogSampling))
	}

	return h.service.CreateSessionToken(ctx, sessionID)
}

// POST /v1/client/sessions/{sessionID}/tokens/{templateName}
func (h *HTTP) CreateFromTemplate(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	templateName := chi.URLParam(r, "templateName")
	sessionID := chi.URLParam(r, "sessionID")

	// Support functionality of `POST /v1/me/tokens` for firebase until we deprecate the endpoint
	if templateName == "integration_firebase" {
		return h.service.CreateFromFirebase(r.Context(), sessionID)
	}

	return h.service.CreateFromTemplate(
		r.Context(),
		templateName,
		sessionID,
		r.Header.Get("Origin"),
	)
}
