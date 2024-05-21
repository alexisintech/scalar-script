package sign_up

import (
	"context"
	"net/http"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/clients"
	"clerk/api/fapi/v1/cookies"
	"clerk/api/fapi/v1/wrapper"
	"clerk/api/serialize"
	"clerk/api/shared/sign_up"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/externalapis/turnstile"
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
	db      database.Database
	clock   clockwork.Clock
	cookies *cookies.CookieSetter
	wrapper *wrapper.Wrapper

	// services
	clientService *clients.Service
	service       *Service
	signUpService *sign_up.Service
}

func NewHTTP(deps clerk.Deps, captchaClientPool *turnstile.ClientPool) *HTTP {
	return &HTTP{
		db:            deps.DB(),
		clock:         deps.Clock(),
		cookies:       cookies.NewCookieSetter(deps),
		wrapper:       wrapper.NewWrapper(deps),
		clientService: clients.NewService(deps),
		service:       NewService(deps, captchaClientPool),
		signUpService: sign_up.NewService(deps),
	}
}

// POST /v1/client/sign_ups
func (h *HTTP) Create(w http.ResponseWriter, r *http.Request) (_ interface{}, retErr apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	var client *model.Client

	defer func() {
		if retErr != nil {
			retErr = h.wrapper.WrapError(ctx, retErr, client)
		}
	}()

	createForm := &SignUpForm{
		Transfer:                  form.GetBool(r.Form, param.Transfer.Name),
		Password:                  form.GetStringOrNil(r.Form, param.Password.Name),
		FirstName:                 form.GetStringOrNil(r.Form, param.FirstName.Name),
		LastName:                  form.GetStringOrNil(r.Form, param.LastName.Name),
		Username:                  form.GetStringOrNil(r.Form, param.Username.Name),
		EmailAddress:              form.GetStringOrNil(r.Form, param.EmailAddress.Name),
		PhoneNumber:               form.GetStringOrNil(r.Form, param.PhoneNumber.Name),
		EmailAddressOrPhoneNumber: form.GetStringOrNil(r.Form, param.EmailAddressOrPhoneNumber.Name),
		UnsafeMetadata:            form.GetJSON(r.Form, param.UnsafeMetadata.Name),
		Strategy:                  form.GetStringOrNil(r.Form, param.Strategy.Name),
		RedirectURL:               form.GetStringOrNil(r.Form, param.RedirectURL.Name),
		ActionCompleteRedirectURL: form.GetStringOrNil(r.Form, param.ActionCompleteRedirectURL.Name),
		Ticket:                    form.GetStringOrNil(r.Form, param.Ticket.Name),
		Web3Wallet:                form.GetStringOrNil(r.Form, param.Web3Wallet.Name),
		Token:                     form.GetStringOrNil(r.Form, param.Token.Name),
		Origin:                    r.Header.Get("Origin"),
		CaptchaToken:              form.GetStringOrNil(r.Form, param.CaptchaToken.Name),
		CaptchaError:              form.GetStringOrNil(r.Form, param.CaptchaError.Name),
		CaptchaWidgetType:         form.GetStringOrNil(r.Form, param.CaptchaWidgetType.Name),
	}
	signUp, client, newClientCreated, err := h.service.Create(ctx, createForm)
	if err != nil {
		return nil, err
	}

	// if new client, add it to the logline so that it's included in our logs
	if newClientCreated {
		log.AddToLogLine(ctx, log.ClientID, client.ID)
		log.AddToLogLine(ctx, log.RotatingToken, client.RotatingToken)
	}

	signUpResponse, err := h.toResponse(ctx, signUp, userSettings)
	if err != nil {
		return nil, err
	}

	if !newClientCreated {
		return h.wrapper.WrapResponse(ctx, signUpResponse, client)
	}

	return h.cookies.RespondWithCookie(ctx, w, r, client, signUpResponse, nil)
}

// GET /v1/client/sign_ups/{signUpID}
func (h *HTTP) Read(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	signUp := sign_up.FromContext(ctx)

	signUpResponse, err := h.toResponse(ctx, signUp, userSettings)
	if err != nil {
		return nil, err
	}

	// The rotating_token_nonce is used in native application oauth flows to allow the native client
	// to update its JWT once despite changes in its rotating_token. The rotating_token_nonce is exchanged
	// once for the updated client JWT via a GET /v1/clients/sign_ups/:id. Hence the need to use RespondWithCookie.
	rotatingTokenNonce := ctx.Value(ctxkeys.RotatingTokenNonce).(string)
	if rotatingTokenNonce != "" {
		return h.cookies.RespondWithCookie(ctx, w, r, client, signUpResponse, err)
	}

	return h.wrapper.WrapResponse(ctx, signUpResponse, client)
}

// PATCH /v1/client/sign_ups/{signUpID}
func (h *HTTP) Update(w http.ResponseWriter, r *http.Request) (_ interface{}, retErr apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	existingClient := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	defer func() {
		if retErr != nil {
			retErr = h.wrapper.WrapError(ctx, retErr, existingClient)
		}
	}()

	updateForm := &SignUpForm{
		Password:                  form.GetStringOrNil(r.Form, param.Password.Name),
		FirstName:                 form.GetStringOrNil(r.Form, param.FirstName.Name),
		LastName:                  form.GetStringOrNil(r.Form, param.LastName.Name),
		Username:                  form.GetStringOrNil(r.Form, param.Username.Name),
		EmailAddress:              form.GetStringOrNil(r.Form, param.EmailAddress.Name),
		PhoneNumber:               form.GetStringOrNil(r.Form, param.PhoneNumber.Name),
		EmailAddressOrPhoneNumber: form.GetStringOrNil(r.Form, param.EmailAddressOrPhoneNumber.Name),
		UnsafeMetadata:            form.GetJSON(r.Form, param.UnsafeMetadata.Name),
		Strategy:                  form.GetStringOrNil(r.Form, param.Strategy.Name),
		RedirectURL:               form.GetStringOrNil(r.Form, param.RedirectURL.Name),
		ActionCompleteRedirectURL: form.GetStringOrNil(r.Form, param.ActionCompleteRedirectURL.Name),
		Ticket:                    form.GetStringOrNil(r.Form, param.Ticket.Name),
		Web3Wallet:                form.GetStringOrNil(r.Form, param.Web3Wallet.Name),
		Token:                     form.GetStringOrNil(r.Form, param.Token.Name),
		Origin:                    r.Header.Get("Origin"),
	}
	signUp, newClient, err := h.service.Update(ctx, updateForm)
	if err != nil {
		return nil, err
	}

	signUpResponse, err := h.toResponse(ctx, signUp, userSettings)
	if err != nil {
		return nil, err
	}

	if newClient == nil {
		return h.wrapper.WrapResponse(ctx, signUpResponse, existingClient)
	}

	return h.cookies.RespondWithCookie(ctx, w, r, newClient, signUpResponse, nil)
}

func (h *HTTP) toResponse(
	ctx context.Context,
	signUp *model.SignUp,
	userSettings *usersettings.UserSettings,
) (*serialize.SignUpResponse, apierror.Error) {
	signUpSerializable, err := h.signUpService.ConvertToSerializable(ctx, h.db, signUp, userSettings, "")
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response, err := serialize.SignUp(ctx, h.clock, signUpSerializable)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return response, nil
}

// POST /v1/client/sign_ups/{signUpID}/prepare_verification
func (h *HTTP) PrepareVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	// Check there are no parameters on the form
	requiredParams := param.NewSet(param.Strategy)
	optionalParams := param.NewSet(param.RedirectURL, param.ActionCompleteRedirectURL)
	pl := param.NewList(requiredParams, optionalParams)
	err := form.Check(r.Form, pl)

	if err != nil {
		return nil, err
	}

	prepareForm := &SignUpPrepareForm{
		Strategy:                  r.Form.Get(param.Strategy.Name),
		RedirectURL:               form.GetString(r.Form, param.RedirectURL.Name),
		ActionCompleteRedirectURL: form.GetString(r.Form, param.ActionCompleteRedirectURL.Name),
		Origin:                    r.Header.Get("Origin"),
	}
	signUp, err := h.service.PrepareVerification(ctx, prepareForm)
	if apierror.IsInternal(err) {
		return nil, err
	} else if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	signUpResponse, err := h.toResponse(ctx, signUp, userSettings)
	if err != nil {
		return nil, err
	}

	return h.wrapper.WrapResponse(ctx, signUpResponse, client)
}

// POST /v1/client/sign_ups/{signUpID}/attempt_verification
func (h *HTTP) AttemptVerification(w http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	reqParams := param.NewSet(param.Strategy)
	optParams := param.NewSet(param.Code, param.Web3Signature, param.Token)

	pl := param.NewList(reqParams, optParams)
	err := form.Check(r.Form, pl)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	attemptForm := strategies.SignUpAttemptForm{
		Strategy:      r.Form.Get(param.Strategy.Name),
		Code:          form.GetString(r.Form, param.Code.Name),
		Web3Signature: form.GetString(r.Form, param.Web3Signature.Name),
		Token:         form.GetStringOrNil(r.Form, param.Token.Name),
	}

	signUp, createdNewSession, err := h.service.AttemptVerification(ctx, attemptForm)
	if apierror.IsInternal(err) {
		return nil, err
	} else if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	signUpResponse, err := h.toResponse(ctx, signUp, userSettings)
	if err != nil {
		return nil, err
	}

	if !createdNewSession {
		return h.wrapper.WrapResponse(ctx, signUpResponse, client)
	}

	return h.cookies.RespondWithCookie(ctx, w, r, client, signUpResponse, nil)
}

// SetSignUpFromPath is a HTTP middleware that reads the signUpID URL parameter
// and sets the sign up object specified by that ID on the http.Request
// context.
// The current client must already be set on the http.Request context.
func (h *HTTP) SetSignUpFromPath(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	ctx, err := h.service.SetSignUp(r.Context(), chi.URLParam(r, "signUpID"))
	if err != nil {
		return r, err
	}
	return r.WithContext(ctx), nil
}

// EnsureLatestSignUp is a HTTP middleware to ensure that the sign up in the
// http.Request context is the latest sign up on the client.
func (h *HTTP) EnsureLatestSignUp(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	client := r.Context().Value(ctxkeys.RequestingClient).(*model.Client)
	signUp := sign_up.FromContext(r.Context())

	if !client.SignUpID.Valid {
		return r, apierror.InvalidClientStateForAction("a get", "No sign up.")
	}

	if client.SignUpID.String != signUp.ID {
		return r, apierror.SignUpForbiddenAccess()
	}

	return r, nil
}
