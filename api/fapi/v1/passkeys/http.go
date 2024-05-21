package passkeys

import (
	"clerk/api/apierror"
	"clerk/api/fapi/v1/users"
	"clerk/api/fapi/v1/wrapper"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/usersettings/clerk/strategies"
	clerkwebauthn "clerk/pkg/webauthn"
	"clerk/utils/clerk"
	"clerk/utils/form"
	"clerk/utils/param"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	service      *Service
	usersService *users.Service
	wrapper      *wrapper.Wrapper
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		service:      NewService(deps),
		usersService: users.NewService(deps),
		wrapper:      wrapper.NewWrapper(deps),
	}
}

// POST /v1/me/passkeys
func (h *HTTP) CreatePasskey(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	if formErrs := form.CheckEmpty(r.Form); formErrs != nil {
		return nil, formErrs
	}

	// construct initial passkey name
	passkeyName := clerkwebauthn.CreatePasskeyName(r.Header)

	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = "https://" + r.Header.Get(constants.XOriginalHost)
	}

	resp, err := h.service.CreatePasskey(ctx, user, origin, passkeyName)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// POST /v1/me/passkeys/{passkeyID}/attempt_verification
func (h *HTTP) AttemptPasskeyVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.PublicKeyCredential), param.NewSet(param.Strategy)))
	if err != nil {
		return nil, err
	}

	passkeyID := chi.URLParam(r, "passkeyIdentID")
	origin := r.Header.Get("Origin")
	attemptForm := strategies.VerificationAttemptForm{
		Strategy:                   constants.VSPasskey,
		PasskeyIdentID:             &passkeyID,
		PasskeyPublicKeyCredential: form.GetString(r.Form, param.PublicKeyCredential.Name),
		Origin:                     &origin,
	}

	resp, err := h.usersService.AttemptVerification(ctx, user, attemptForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// GET /v1/me/passkeys/{passkeyIdentID}
func (h *HTTP) ReadPasskey(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	user := requesting_user.FromContext(ctx)
	passkeyIdentID := chi.URLParam(r, "passkeyIdentID")

	passkeyIdent, err := h.service.ReadPasskey(ctx, user, passkeyIdentID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, passkeyIdent, client)
}

// PATCH /v1/me/passkeys/{passkeyIdentID}
func (h *HTTP) UpdatePasskey(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	reqParams := param.NewSet()
	optParams := param.NewSet(param.PasskeyName)
	formErrs := form.Check(r.Form, param.NewList(reqParams, optParams))
	if formErrs != nil {
		return nil, h.wrapper.WrapError(ctx, formErrs, client)
	}

	user := requesting_user.FromContext(ctx)
	passkeyIdentID := chi.URLParam(r, "passkeyIdentID")

	passkeyIdent, err := h.service.UpdatePasskey(ctx, user, passkeyIdentID, form.GetString(r.Form, param.PasskeyName.Name))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, passkeyIdent, client)
}

// DELETE /v1/me/passkeys/{passkeyIdentID}
func (h *HTTP) DeletePasskey(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if formErrs := form.CheckEmpty(r.Form); formErrs != nil {
		return nil, formErrs
	}

	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)
	passkeyIdentID := chi.URLParam(r, "passkeyIdentID")

	response, err := h.service.DeletePasskey(ctx, user, passkeyIdentID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, response, client)
}
