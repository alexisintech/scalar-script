package sign_in

import (
	"context"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/clients"
	"clerk/api/fapi/v1/cookies"
	"clerk/api/fapi/v1/wrapper"
	"clerk/api/serialize"
	"clerk/api/shared/sign_in"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/strategies"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/form"
	"clerk/utils/log"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
)

type HTTP struct {
	db    database.Database
	clock clockwork.Clock

	clientService *clients.Service
	cookies       *cookies.CookieSetter
	service       *Service
	signInService *sign_in.Service
	wrapper       *wrapper.Wrapper
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		db:            deps.DB(),
		clock:         deps.Clock(),
		clientService: clients.NewService(deps),
		cookies:       cookies.NewCookieSetter(deps),
		service:       NewService(deps),
		signInService: sign_in.NewService(deps),
		wrapper:       wrapper.NewWrapper(deps),
	}
}

func (h *HTTP) SetSignInFromPath(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	signInID := chi.URLParam(r, "signInID")
	newCtx, err := h.service.SetSignIn(r.Context(), signInID)
	return r.WithContext(newCtx), err
}

func (h *HTTP) EnsureUserNotLocked(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx := r.Context()
	signIn := ctx.Value(ctxkeys.SignIn).(*model.SignIn)

	apiErr := h.service.EnsureUserNotLockedFromSignIn(r.Context(), h.db, signIn)
	if apiErr != nil {
		return nil, apiErr
	}

	return r, nil
}

// Middleware /v1/client/sign_ins/{signInID}
func (h *HTTP) EnsureLatestClientSignIn(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	err := h.service.EnsureLatestClientSignIn(r.Context())
	if err != nil {
		return nil, err
	}
	return r, nil
}

// GET /v1/client/sign_ins/{signInID}
func (h *HTTP) Read(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	signIn := ctx.Value(ctxkeys.SignIn).(*model.SignIn)

	signInResponse, err := h.toResponse(ctx, signIn, userSettings)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	// The rotating_token_nonce is used in native application oauth flows to allow the native client
	// to update its JWT once despite changes in its rotating_token. The rotating_token_nonce is exchanged
	// once for the updated client JWT via a GET /v1/clients/sign_ins/:id. Hence the need to use RespondWithCookie.
	rotatingTokenNonce := ctx.Value(ctxkeys.RotatingTokenNonce).(string)
	if rotatingTokenNonce != "" {
		return h.cookies.RespondWithCookie(ctx, w, r, client, signInResponse, err)
	}

	return h.wrapper.WrapResponse(ctx, signInResponse, client)
}

// POST /v1/client/sign_ins
func (h *HTTP) Create(w http.ResponseWriter, r *http.Request) (_ interface{}, retErr apierror.Error) {
	ctx := r.Context()
	currentClient, _ := h.clientService.GetClientFromContext(ctx)
	var newClient *model.Client

	defer func() {
		if retErr != nil {
			if newClient != nil {
				retErr = h.wrapper.WrapError(ctx, retErr, newClient)
			} else {
				retErr = h.wrapper.WrapError(ctx, retErr, currentClient)
			}
		}
	}()

	reqParams := param.NewSet()
	optParams := param.NewSet(
		param.Identifier.NilableCopy(),
		param.Strategy,
		param.Password,
		param.RedirectURL,
		param.ActionCompleteRedirectURL,
		param.Transfer,
		param.UnsafeMetadata,
		param.Ticket,
		param.Token,
	)

	pl := param.NewList(reqParams, optParams)
	err := form.Check(r.Form, pl)
	if err != nil {
		return nil, err
	}

	var identifier *string
	formIdentifier := form.GetNullString(r.Form, param.Identifier.Name)
	if formIdentifier != nil {
		identifier = &formIdentifier.String
	}

	createForm := SignInCreateForm{
		Transfer:                  form.GetBool(r.Form, param.Transfer.Name),
		Identifier:                identifier,
		Strategy:                  form.GetString(r.Form, param.Strategy.Name),
		RedirectURL:               form.GetString(r.Form, param.RedirectURL.Name),
		ActionCompleteRedirectURL: form.GetString(r.Form, param.ActionCompleteRedirectURL.Name),
		Password:                  form.GetString(r.Form, param.Password.Name),
		Ticket:                    form.GetString(r.Form, param.Ticket.Name),
		Token:                     form.GetString(r.Form, param.Token.Name),
		Origin:                    r.Header.Get("Origin"),
	}

	signIn, newClient, serviceErr := h.service.Create(ctx, createForm)
	if serviceErr != nil && (signIn == nil || newClient == nil) {
		return nil, serviceErr
	}

	// if there is a new client, add it to the logline so that it's included in our logs
	if newClient != nil {
		log.AddToLogLine(ctx, log.ClientID, newClient.ID)
		log.AddToLogLine(ctx, log.RotatingToken, newClient.RotatingToken)
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	signInResponse, err := h.toResponse(ctx, signIn, userSettings)
	if err != nil {
		return nil, err
	}

	if serviceErr != nil {
		return h.cookies.RespondWithCookie(ctx, w, r, newClient, signInResponse, serviceErr)
	}

	if newClient == nil {
		return h.wrapper.WrapResponse(ctx, signInResponse, currentClient)
	}

	return h.cookies.RespondWithCookie(ctx, w, r, newClient, signInResponse, nil)
}

// POST /v1/client/sign_ins/{signInID}/reset_password
func (h *HTTP) ResetPassword(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	reqParams := param.NewSet(param.Password)
	optionalParams := param.NewSet(param.SignOutOfOtherSessions)
	params := param.NewList(reqParams, optionalParams)
	err := form.Check(r.Form, params)
	if err != nil {
		return nil, err
	}

	signIn, newClient, err := h.service.ResetPassword(ctx, ResetPasswordParams{
		Password:               *form.GetString(r.Form, param.Password.Name),
		SignOutOfOtherSessions: form.GetBoolOrFallback(r.Form, param.SignOutOfOtherSessions.Name, false),
	})
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	signInResponse, err := h.toResponse(ctx, signIn, userSettings)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	if newClient == nil {
		return h.wrapper.WrapResponse(ctx, signInResponse, client)
	}

	return h.cookies.RespondWithCookie(ctx, w, r, newClient, signInResponse, nil)
}

// POST /v1/client/sign_in/prepare_first_factor
func (h *HTTP) PrepareFirstFactor(_ http.ResponseWriter, r *http.Request) (_ interface{}, retErr apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	defer func() {
		if retErr != nil {
			retErr = h.wrapper.WrapError(ctx, retErr, client)
		}
	}()

	reqParams := param.NewSet(
		param.Strategy,
	)

	optParams := param.NewSet(
		param.RedirectURL,
		param.ActionCompleteRedirectURL,
		param.EmailAddressID,
		param.PhoneNumberID,
		param.Web3WalletID,
	)

	pl := param.NewList(reqParams, optParams)
	err := form.Check(r.Form, pl)
	if err != nil {
		return nil, err
	}

	prepareForm := strategies.SignInPrepareForm{
		Strategy:                  *form.GetString(r.Form, param.Strategy.Name),
		EmailAddressID:            form.GetString(r.Form, param.EmailAddressID.Name),
		PhoneNumberID:             form.GetString(r.Form, param.PhoneNumberID.Name),
		Web3WalletID:              form.GetString(r.Form, param.Web3WalletID.Name),
		RedirectURL:               form.GetString(r.Form, param.RedirectURL.Name),
		ActionCompleteRedirectURL: form.GetString(r.Form, param.ActionCompleteRedirectURL.Name),
		Origin:                    r.Header.Get("Origin"),
		ClientID:                  client.ID,
	}

	signIn, err := h.service.PrepareFirstFactor(ctx, prepareForm)
	if err != nil {
		return nil, err
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	signInResponse, err := h.toResponse(ctx, signIn, userSettings)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, apierror.Unexpected(err), client)
	}

	return h.wrapper.WrapResponse(ctx, signInResponse, client)
}

// POST /v1/client/sign_in/attempt_first_factor
func (h *HTTP) AttemptFirstFactor(w http.ResponseWriter, r *http.Request) (_ interface{}, retErr apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	defer func() {
		if retErr != nil {
			retErr = h.wrapper.WrapError(ctx, retErr, client)
		}
	}()

	reqParamsSet := param.NewSet(
		param.Strategy,
	)

	optParamsSet := param.NewSet(
		param.Password,
		param.Code,
		param.Web3Signature,
		param.Ticket,
		param.PublicKeyCredential,
		param.Token,
	)

	pl := param.NewList(reqParamsSet, optParamsSet)
	formErrs := form.Check(r.Form, pl)
	if formErrs != nil {
		return nil, formErrs
	}

	attemptForm := strategies.SignInAttemptForm{
		Strategy:                   *form.GetString(r.Form, param.Strategy.Name),
		Password:                   form.GetString(r.Form, param.Password.Name),
		Code:                       form.GetString(r.Form, param.Code.Name),
		Web3Signature:              form.GetString(r.Form, param.Web3Signature.Name),
		Ticket:                     form.GetString(r.Form, param.Ticket.Name),
		PasskeyPublicKeyCredential: form.GetString(r.Form, param.PublicKeyCredential.Name),
		Token:                      form.GetString(r.Form, param.Token.Name),
		Origin:                     r.Header.Get("Origin"),
	}

	signIn, newClient, err := h.service.AttemptFirstFactor(ctx, attemptForm)
	if err != nil {
		return nil, err
	}

	signInResponse, err := h.toResponse(ctx, signIn, userSettings)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if newClient == nil {
		return h.wrapper.WrapResponse(ctx, signInResponse, client)
	}

	return h.cookies.RespondWithCookie(ctx, w, r, newClient, signInResponse, nil)
}

// POST /v1/client/sign_in/prepare_second_factor
func (h *HTTP) PrepareSecondFactor(_ http.ResponseWriter, r *http.Request) (_ interface{}, retErr apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	defer func() {
		if retErr != nil {
			retErr = h.wrapper.WrapError(ctx, retErr, client)
		}
	}()

	reqParamsSet := param.NewSet(
		param.Strategy,
	)
	optParamsSet := param.NewSet(
		param.PhoneNumberID,
	)

	pl := param.NewList(reqParamsSet, optParamsSet)
	err := form.Check(r.Form, pl)
	if err != nil {
		return nil, err
	}

	prepareForm := strategies.SignInPrepareForm{
		Strategy:      *form.GetString(r.Form, param.Strategy.Name),
		PhoneNumberID: form.GetString(r.Form, param.PhoneNumberID.Name),
		ClientID:      client.ID,
	}

	signIn, err := h.service.PrepareSecondFactor(ctx, prepareForm)
	if err != nil {
		return nil, err
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	signInResponse, err := h.toResponse(ctx, signIn, userSettings)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, apierror.Unexpected(err), client)
	}

	return h.wrapper.WrapResponse(ctx, signInResponse, client)
}

// POST /v1/client/sign_in/attempt_second_factor
func (h *HTTP) AttemptSecondFactor(w http.ResponseWriter, r *http.Request) (_ interface{}, retErr apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	defer func() {
		if retErr != nil {
			retErr = h.wrapper.WrapError(ctx, retErr, client)
		}
	}()

	reqParamsSet := param.NewSet(param.Strategy)
	optParamsSet := param.NewSet(param.Code)

	pl := param.NewList(reqParamsSet, optParamsSet)
	formErrs := form.Check(r.Form, pl)
	if formErrs != nil {
		return nil, formErrs
	}

	attemptForm := strategies.SignInAttemptForm{
		Strategy: *form.GetString(r.Form, param.Strategy.Name),
		Code:     form.GetString(r.Form, param.Code.Name),
	}

	signIn, newClient, err := h.service.AttemptSecondFactor(ctx, attemptForm)
	if err != nil {
		return nil, err
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	signInResponse, err := h.toResponse(ctx, signIn, userSettings)
	if err != nil {
		return nil, err
	}

	if newClient == nil {
		return h.wrapper.WrapResponse(ctx, signInResponse, client)
	}

	return h.cookies.RespondWithCookie(ctx, w, r, newClient, signInResponse, nil)
}

func (h *HTTP) toResponse(ctx context.Context, signIn *model.SignIn, userSettings *usersettings.UserSettings) (*serialize.SignInResponse, apierror.Error) {
	signInSerializable, err := h.signInService.ConvertToSerializable(ctx, h.db, signIn, userSettings, "")
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response, err := serialize.SignIn(h.clock, signInSerializable, userSettings)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return response, nil
}
