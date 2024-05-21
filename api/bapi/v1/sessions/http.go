package sessions

import (
	"context"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/shared/pagination"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/environment"
	"clerk/utils/clerk"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

// HTTP is the http layer for all requests related to sessions in server API.
// Its responsibility is to extract any relevant information required by the service layer from the incoming request.
// It's also responsible for verifying the correctness of the incoming payload.
type HTTP struct {
	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
	}
}

// GET /v1/sessions
func (h *HTTP) ReadAll(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := readAllParams{
		clientID: clerkhttp.GetOptionalQueryParam(r, "client_id"),
		userID:   clerkhttp.GetOptionalQueryParam(r, "user_id"),
		status:   clerkhttp.GetOptionalQueryParam(r, "status"),
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	if r.URL.Query().Get(param.Paginated.Name) == "true" {
		return h.service.ReadAllPaginated(r.Context(), params, paginationParams)
	}
	return h.service.ReadAll(r.Context(), params, paginationParams)
}

// GET /v1/sessions/{sessionID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	sessionID := chi.URLParam(r, "sessionID")
	return h.service.Read(r.Context(), sessionID)
}

// POST /v1/sessions/{sessionID}/revoke
func (h *HTTP) Revoke(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	sessionID := chi.URLParam(r, "sessionID")
	return h.service.Revoke(r.Context(), sessionID)
}

// POST /v1/sessions/{sessionID}/verify
func (h *HTTP) Verify(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if deprecatedEndpoint(r.Context(), cenv.ClerkSkipBAPIEndpointSessionsVerifyDeprecationApplicationIDs) {
		return nil, apierror.BAPIEndpointDeprecated("This endpoint is deprecated and will be removed in future versions. We strongly recommend switching to networkless verification using short-lived session tokens, " +
			"which is implemented transparently in all recent SDK versions (e.g. NodeJS SDK https://clerk.com/docs/backend-requests/handling/nodejs#clerk-express-require-auth). " +
			"For more details on how networkless verification works, refer to our Session Tokens documentation https://clerk.com/docs/backend-requests/resources/session-tokens.")
	}
	params := VerifyParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	params.SessionID = chi.URLParam(r, "sessionID")

	return h.service.Verify(r.Context(), params)
}

func deprecatedEndpoint(ctx context.Context, allowlistKey string) bool {
	if !cenv.IsSet(allowlistKey) {
		return false
	}
	env := environment.FromContext(ctx)
	return !cenv.ResourceHasAccess(allowlistKey, env.Instance.ApplicationID)
}

// POST /v1/sessions/{sessionID}/tokens/{templateName}
func (h *HTTP) CreateTokenFromTemplate(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	sessionID := chi.URLParam(r, "sessionID")
	templateName := chi.URLParam(r, "templateName")
	return h.service.CreateTokenFromTemplate(r.Context(), sessionID, templateName)
}
