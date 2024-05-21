package sign_ups

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/pkg/clerkhttp"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

// HTTP is the http layer for all requests related to phone numbers in server API.
// Its responsibility is to extract any relevant information required by the service layer from the incoming request.
// It's also responsible for verifying the correctness of the incoming payload.
type HTTP struct {
	db    database.Database
	clock clockwork.Clock

	service *Service
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		db:      deps.DB(),
		clock:   deps.Clock(),
		service: NewService(deps),
	}
}

// Middleware /v1/sign_ups/{signUpID}
func (h *HTTP) CheckSignUpInInstance(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)

	signUpID := chi.URLParam(r, "signUpID")
	err := h.service.CheckSignUpInInstance(ctx, signUpID, env.Instance.ID)
	return r, err
}

// Read - GET /v1/sign_ups/:id
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	signUpID := chi.URLParam(r, "signUpID")
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	return h.service.Read(ctx, userSettings, signUpID)
}

// Update - PATCH /v1/sign_ups/:id
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	params := UpdateParams{}
	if err := clerkhttp.Decode(r, &params); err != nil {
		return nil, err
	}

	signUpID := chi.URLParam(r, "signUpID")

	return h.service.Update(r.Context(), signUpID, &params)
}
