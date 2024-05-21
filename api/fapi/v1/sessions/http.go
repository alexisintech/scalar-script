package sessions

import (
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/cookies"
	"clerk/api/fapi/v1/wrapper"
	"clerk/model"
	"clerk/pkg/ctx/activity"
	"clerk/pkg/ctx/requesting_session"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/ctxkeys"
	"clerk/utils/clerk"
	"clerk/utils/form"
	"clerk/utils/log"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

// Form parameters used in session related HTTP requests.
var (
	paramActiveOrganizationID = param.NewSingle(param.T.String, "active_organization_id", nil)
)

type HTTP struct {
	service *Service
	wrapper *wrapper.Wrapper
	cookies *cookies.CookieSetter
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service: NewService(deps),
		wrapper: wrapper.NewWrapper(deps),
		cookies: cookies.NewCookieSetter(deps),
	}
}

func (h *HTTP) SetRequestingSession(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	sessionID := chi.URLParam(r, "sessionID")
	session, err := h.service.GetCurrentClientSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	log.AddToLogLine(ctx, log.SessionID, session.ID)
	log.AddToLogLine(ctx, log.UserID, session.UserID)

	actorID, actorError := session.ActorID()
	if actorError != nil {
		return r.WithContext(ctx), apierror.Unexpected(actorError)
	}
	if actorID != nil {
		log.AddToLogLine(ctx, log.ActorID, actorID)
	}

	newCtx := requesting_session.NewContext(ctx, session)
	return r.WithContext(newCtx), nil
}

// GET /v1/client/sessions/{sessionID}
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	sessionID := chi.URLParam(r, "sessionID")
	session, err := h.service.Read(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	return h.wrapper.WrapResponse(ctx, session, client)
}

// POST /v1/client/sessions/{sessionID}/touch
func (h *HTTP) Touch(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	err := form.Check(r.Form, param.NewList(param.NewSet(), param.NewSet(paramActiveOrganizationID.NilableCopy())))
	if err != nil {
		return nil, err
	}

	session, err := h.service.Touch(ctx, TouchParams{
		SessionID:            chi.URLParam(r, "sessionID"),
		ActiveOrganizationID: form.GetNullString(r.Form, paramActiveOrganizationID.Name),
		Activity:             activity.FromContext(ctx),
	})
	if err != nil {
		return nil, err
	}

	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	return h.wrapper.WrapResponse(ctx, session, client)
}

// POST /v1/client/sessions/{sessionID}/end
func (h *HTTP) End(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	sessionID := chi.URLParam(r, "sessionID")
	session, err := h.service.End(ctx, client, sessionID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.cookies.RespondWithCookie(ctx, w, r, client, session, err)
}

// POST /v1/client/sessions/{sessionID}/remove
func (h *HTTP) Remove(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	sessionID := chi.URLParam(r, "sessionID")
	session, err := h.service.Remove(ctx, sessionID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, session, client)
}

// POST /v1/me/sessions/{sessionID}/revoke
func (h *HTTP) Revoke(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	sessionID := chi.URLParam(r, "sessionID")
	session, err := h.service.Revoke(ctx, sessionID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, session, client)
}

// GET /v1/me/sessions
func (h *HTTP) ListUserSessions(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	user := requesting_user.FromContext(ctx)
	return h.service.ListUserSessions(ctx, user.ID)
}

// GET /v1/me/sessions/active
func (h *HTTP) ListUserActiveSessions(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	user := requesting_user.FromContext(ctx)
	return h.service.ListUserActiveSessions(r.Context(), user.ID)
}
