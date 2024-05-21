package users

import (
	"net/http"
	"strings"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/wrapper"
	"clerk/api/shared/images"
	"clerk/api/shared/pagination"
	"clerk/api/shared/phone_numbers"
	"clerk/api/shared/users"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/ctxkeys"
	clerkjson "clerk/pkg/json"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/pkg/usersettings/clerk/strategies"
	"clerk/utils/clerk"
	"clerk/utils/form"
	"clerk/utils/param"

	"github.com/go-chi/chi/v5"
)

type HTTP struct {
	userService         *Service
	phoneNumbersService *phone_numbers.Service
	sharedUserService   *users.Service
	wrapper             *wrapper.Wrapper
}

func NewHTTP(deps clerk.Deps) *HTTP {
	return &HTTP{
		userService:         NewService(deps),
		phoneNumbersService: phone_numbers.NewService(deps),
		sharedUserService:   users.NewService(deps),
		wrapper:             wrapper.NewWrapper(deps),
	}
}

func (h *HTTP) SetRequestingUser(_ http.ResponseWriter, r *http.Request) (*http.Request, apierror.Error) {
	// This session id is only useful for multi-session instances because in single-session ones, a user has a single session.
	// The reason that this parsing is common for both cases is that this `clerk.RequestParse` also removes the value
	// from the form. This is important, because if the value stays there, further form validation will fail since they won't expect it.
	userSessionID := requestParse(r, "Clerk-Session-Id")

	// This line ensures backwards compatibility with Clerk JS versions <= 1.9.0.
	// These versions were using _clerk_session querystring parameter for user restricted endpoints.
	// A month after this commit, please delete the following lines if you pass by.
	if userSessionID == nil {
		userSessionID = requestParse(r, "Clerk-Session")
	}

	newCtx, err := h.userService.SetRequestingUser(r.Context(), userSessionID)
	if err != nil {
		return nil, err
	}
	return r.WithContext(newCtx), err
}

// GET /v1/me
func (h *HTTP) Read(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	user := requesting_user.FromContext(ctx)
	userResponse, err := h.userService.Read(ctx, user)
	if err != nil {
		return nil, err
	}

	return h.wrapper.WrapResponse(ctx, userResponse, client)
}

// POST /v1/me/profile_image
func (h *HTTP) UpdateProfileImage(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	user := requesting_user.FromContext(ctx)
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	file, apiErr := images.ReadFileOrBase64(r)
	if apiErr != nil {
		return nil, apiErr
	}

	params := users.UpdateProfileImageParams{
		UserID: user.ID,
		Data:   file,
	}
	res, apiErr := h.sharedUserService.UpdateProfileImage(ctx, params, env.Instance, userSettings)
	if apiErr != nil {
		return nil, apiErr
	}

	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	return h.wrapper.WrapResponse(ctx, res, client)
}

// DELETE /v1/me/profile_image
func (h *HTTP) DeleteProfileImage(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	user := requesting_user.FromContext(ctx)

	res, err := h.userService.DeleteProfileImage(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	return h.wrapper.WrapResponse(ctx, res, client)
}

// PATCH /v1/me
func (h *HTTP) Update(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	paramList := getUpdateUserParamList(userSettings)
	err := form.Check(r.Form, paramList)
	if err != nil {
		return nil, err
	}

	updateForm := users.UpdateForm{
		FirstName:             getJSONString(r.Form, param.FirstName.Name),
		LastName:              getJSONString(r.Form, param.LastName.Name),
		Username:              getJSONString(r.Form, param.Username.Name),
		Password:              form.GetString(r.Form, param.Password.Name),
		PrimaryEmailAddressID: form.GetString(r.Form, param.PrimaryEmailAddressID.Name),
		PrimaryPhoneNumberID:  form.GetString(r.Form, param.PrimaryPhoneNumberID.Name),
		PrimaryWeb3WalletID:   form.GetString(r.Form, param.PrimaryWeb3WalletID.Name),
		PublicMetadata:        form.GetJSONRawMessage(r.Form, param.PublicMetadata.Name),
		PrivateMetadata:       form.GetJSONRawMessage(r.Form, param.PrivateMetadata.Name),
		UnsafeMetadata:        form.GetJSONRawMessage(r.Form, param.UnsafeMetadata.Name),
		ProfileImageID:        form.GetString(r.Form, param.ProfileImageID.Name),
	}
	res, err := h.userService.Update(ctx, updateForm)
	if err != nil {
		return nil, err
	}

	return h.wrapper.WrapResponse(ctx, res, client)
}

func getUpdateUserParamList(userSettings *usersettings.UserSettings) *param.List {
	optParams := param.NewSet()
	reqParams := param.NewSet()

	optParams.Add(param.ProfileImageID, param.UnsafeMetadata)

	if userSettings.GetAttribute(names.EmailAddress).Base().Enabled {
		optParams.Add(param.PrimaryEmailAddressID)
	}

	if userSettings.GetAttribute(names.PhoneNumber).Base().Enabled {
		optParams.Add(param.PrimaryPhoneNumberID)
	}

	firstName := userSettings.GetAttribute(names.FirstName).Base()
	if firstName.Required {
		optParams.Add(param.FirstName)
	} else if firstName.Enabled {
		optParams.Add(param.FirstName.NilableCopy())
	}

	lastName := userSettings.GetAttribute(names.LastName).Base()
	if lastName.Required {
		optParams.Add(param.LastName)
	} else if lastName.Enabled {
		optParams.Add(param.LastName.NilableCopy())
	}

	password := userSettings.GetAttribute(names.Password).Base()
	if password.Required {
		optParams.Add(param.Password)
	} else if password.Enabled {
		optParams.Add(param.Password.NilableCopy())
	}

	if userSettings.GetAttribute(names.Username).Base().Enabled {
		optParams.Add(param.Username.NilableCopy())
	}

	return param.NewList(reqParams, optParams)
}

func getJSONString(form map[string][]string, key string) clerkjson.String {
	values, isSet := form[key]
	// key not present
	if !isSet {
		return clerkjson.NewUnsetString()
	}
	// value is blank
	if len(values) < 1 {
		return clerkjson.StringFromPtr(nil)
	}
	// value exists
	return clerkjson.StringFrom(values[0])
}

// DELETE /v1/me
func (h *HTTP) Delete(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	response, err := h.userService.Delete(ctx, user)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, response, client)
}

// GET /v1/me/email_addresses
func (h *HTTP) ListUserEmailAddresses(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	user := requesting_user.FromContext(ctx)
	return h.userService.ListIdentifications(ctx, user.ID, constants.ITEmailAddress)
}

// POST /v1/me/email_addresses
func (h *HTTP) CreateEmailAddress(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	reqParams := param.NewSet(param.EmailAddress)
	optParams := param.NewSet()
	pl := param.NewList(reqParams, optParams)
	err := form.Check(r.Form, pl)
	if err != nil {
		return nil, err
	}

	user := requesting_user.FromContext(ctx)
	emailAddress := *form.GetString(r.Form, param.EmailAddress.Name)

	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	newEmail, err := h.userService.CreateEmailAddress(r.Context(), user, emailAddress)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, newEmail, client)
}

// GET /v1/me/email_addresses/{emailId}
func (h *HTTP) ReadEmailAddress(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	user := requesting_user.FromContext(ctx)
	emailAddressID := chi.URLParam(r, "emailID")

	emailAddress, err := h.userService.ReadIdentification(ctx, user.ID, emailAddressID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, emailAddress, client)
}

// POST /v1/me/email_addresses/{emailId}/prepare_verification
func (h *HTTP) PrepareEmailAddressVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()

	pl := param.NewList(param.NewSet(param.Strategy), param.NewSet(param.RedirectURL))
	err := form.Check(r.Form, pl)
	if err != nil {
		return nil, err
	}

	user := requesting_user.FromContext(ctx)
	emailAddressID := chi.URLParam(r, "emailID")

	prepareForm := strategies.VerificationPrepareForm{
		Strategy:       r.Form.Get(param.Strategy.Name),
		EmailAddressID: &emailAddressID,
		RedirectURL:    form.GetString(r.Form, param.RedirectURL.Name),
	}
	email, err := h.userService.PrepareVerification(ctx, user, prepareForm)
	if err != nil {
		return nil, err
	}

	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	return h.wrapper.WrapResponse(ctx, email, client)
}

// POST /v1/me/email_addresses/{emailId}/attempt_verification
func (h *HTTP) AttemptEmailAddressVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	// Check the Form
	reqParams := param.NewSet(param.Code)
	pl := param.NewList(reqParams, param.NewSet())
	formErrs := form.Check(r.Form, pl)
	if formErrs != nil {
		return nil, formErrs
	}

	user := requesting_user.FromContext(ctx)
	emailAddressID := chi.URLParam(r, "emailID")

	attemptForm := strategies.VerificationAttemptForm{
		Strategy:       constants.VSEmailCode,
		EmailAddressID: &emailAddressID,
		Code:           form.GetString(r.Form, param.Code.Name),
	}

	email, err := h.userService.AttemptVerification(ctx, user, attemptForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, email, client)
}

// DELETE /v1/me/email_addresses/{emailId}
func (h *HTTP) DeleteEmailAddress(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if formErrs := form.CheckEmpty(r.Form); formErrs != nil {
		return nil, formErrs
	}

	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)
	emailAddressID := chi.URLParam(r, "emailID")

	emailAddress, err := h.userService.DeleteEmailAddress(ctx, user, emailAddressID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, emailAddress, client)
}

// GET /v1/me/phone_numbers/{phoneNumberId}
func (h *HTTP) ReadPhoneNumber(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	user := requesting_user.FromContext(ctx)
	phoneNumberID := chi.URLParam(r, "phoneNumberID")

	phoneNumber, err := h.userService.ReadIdentification(ctx, user.ID, phoneNumberID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, phoneNumber, client)
}

// PATCH /v1/me/phone_numbers/{phoneNumberId}
func (h *HTTP) UpdatePhoneNumber(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	reqParams := param.NewSet()
	optParams := param.NewSet(param.ReservedForSecondFactor, param.DefaultSecondFactor)
	formErrs := form.Check(r.Form, param.NewList(reqParams, optParams))
	if formErrs != nil {
		return nil, h.wrapper.WrapError(ctx, formErrs, client)
	}

	user := requesting_user.FromContext(ctx)
	phoneNumberID := chi.URLParam(r, "phoneNumberID")

	updateMFAForm := phone_numbers.UpdateForMFAForm{
		DefaultSecondFactor:     form.GetBool(r.Form, param.DefaultSecondFactor.Name),
		ReservedForSecondFactor: form.GetBool(r.Form, param.ReservedForSecondFactor.Name),
	}
	phoneNumber, err := h.userService.UpdatePhoneNumber(ctx, user, phoneNumberID, &updateMFAForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, phoneNumber, client)
}

// GET /v1/me/phone_numbers
func (h *HTTP) ListPhoneNumbers(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	user := requesting_user.FromContext(ctx)
	return h.userService.ListIdentifications(ctx, user.ID, constants.ITPhoneNumber)
}

// POST /v1/me/phone_numbers
func (h *HTTP) CreatePhoneNumber(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	reqParams := param.NewSet(param.PhoneNumber)
	optParams := param.NewSet(param.ReservedForSecondFactor)

	pl := param.NewList(reqParams, optParams)
	err := form.Check(r.Form, pl)
	if err != nil {
		return nil, err
	}

	user := requesting_user.FromContext(ctx)

	phoneNumber := *form.GetString(r.Form, param.PhoneNumber.Name)
	reserveForSecondFactor := form.GetBool(r.Form, param.ReservedForSecondFactor.Name)
	newPhoneNumber, err := h.userService.CreatePhoneNumber(ctx, user, phoneNumber, reserveForSecondFactor)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, newPhoneNumber, client)
}

// POST /v1/me/phone_numbers/{phoneNumberId}/prepare_verification
func (h *HTTP) PreparePhoneNumberVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	pl := param.NewList(param.NewSet(param.Strategy), param.NewSet())
	formErrs := form.Check(r.Form, pl)
	if formErrs != nil {
		return nil, formErrs
	}

	user := requesting_user.FromContext(ctx)
	phoneNumberID := chi.URLParam(r, "phoneNumberID")

	prepareForm := strategies.VerificationPrepareForm{
		Strategy:      r.Form.Get(param.Strategy.Name),
		PhoneNumberID: &phoneNumberID,
	}

	phone, err := h.userService.PrepareVerification(ctx, user, prepareForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, phone, client)
}

// POST /v1/me/phone_numbers/{phoneNumberId}/attempt_verification
func (h *HTTP) AttemptPhoneNumberVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	reqParams := param.NewSet(param.Code)
	pl := param.NewList(reqParams, param.NewSet())
	err := form.Check(r.Form, pl)
	if err != nil {
		return nil, err
	}

	user := requesting_user.FromContext(ctx)
	phoneNumberID := chi.URLParam(r, "phoneNumberID")

	attemptForm := strategies.VerificationAttemptForm{
		Strategy:      constants.VSPhoneCode,
		PhoneNumberID: &phoneNumberID,
		Code:          form.GetString(r.Form, param.Code.Name),
	}

	phone, err := h.userService.AttemptVerification(ctx, user, attemptForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, phone, client)
}

// DELETE /v1/me/phone_numbers/{phoneNumberId}
func (h *HTTP) DeletePhoneNumber(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if formErrs := form.CheckEmpty(r.Form); formErrs != nil {
		return nil, formErrs
	}

	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)
	phoneNumberID := chi.URLParam(r, "phoneNumberID")

	phoneNumber, err := h.userService.DeleteIdentification(ctx, user, phoneNumberID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, phoneNumber, client)
}

// GET /v1/me/web3_wallets
func (h *HTTP) ListWeb3Wallets(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	user := requesting_user.FromContext(ctx)
	return h.userService.ListIdentifications(ctx, user.ID, constants.ITWeb3Wallet)
}

// POST /v1/me/web3_wallets
func (h *HTTP) CreateWeb3Wallet(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.Web3Wallet), param.NewSet()))
	if err != nil {
		return nil, err
	}

	web3Wallet := *form.GetString(r.Form, param.Web3Wallet.Name)
	resp, err := h.userService.CreateWeb3Wallet(ctx, web3Wallet)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// GET /v1/me/web3_wallets/{web3WalletID}
func (h *HTTP) ReadWeb3Wallet(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	user := requesting_user.FromContext(ctx)
	web3WalletID := chi.URLParam(r, "web3WalletID")

	web3Wallet, err := h.userService.ReadIdentification(ctx, user.ID, web3WalletID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, web3Wallet, client)
}

// POST /v1/me/web3_wallets/{web3WalletID}/prepare_verification
func (h *HTTP) PrepareWeb3WalletVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.Strategy), param.NewSet(param.RedirectURL)))
	if err != nil {
		return nil, err
	}

	web3WalletID := chi.URLParam(r, "web3WalletID")

	prepareForm := strategies.VerificationPrepareForm{
		Strategy:     r.Form.Get(param.Strategy.Name),
		Web3WalletID: &web3WalletID,
		RedirectURL:  form.GetString(r.Form, param.RedirectURL.Name),
	}
	resp, err := h.userService.PrepareVerification(ctx, user, prepareForm)
	if err != nil {
		return nil, err
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// POST /v1/me/web3_wallets/{web3WalletID}/attempt_verification
func (h *HTTP) AttemptWeb3WalletVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.Web3Signature), param.NewSet()))
	if err != nil {
		return nil, err
	}

	web3WalletID := chi.URLParam(r, "web3WalletID")

	attemptForm := strategies.VerificationAttemptForm{
		Strategy:     constants.VSWeb3MetamaskSignature,
		Web3WalletID: &web3WalletID,
		Code:         form.GetString(r.Form, param.Web3Signature.Name),
	}

	resp, err := h.userService.AttemptVerification(ctx, user, attemptForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// DELETE /v1/me/web3_wallets/{web3WalletID}
func (h *HTTP) DeleteWeb3Wallet(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	web3WalletID := chi.URLParam(r, "web3WalletID")

	resp, err := h.userService.DeleteIdentification(ctx, user, web3WalletID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// POST /v1/me/external_accounts
func (h *HTTP) ConnectOAuthAccount(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	reqParams := param.NewSet(param.Strategy, param.RedirectURL)
	optParams := param.NewSet(param.ActionCompleteRedirectURL, param.AdditionalScope)

	err := form.Check(r.Form, param.NewList(reqParams, optParams))
	if err != nil {
		return nil, err
	}

	createForm := &ConnectOAuthAccountForm{
		Strategy:                  *form.GetString(r.Form, param.Strategy.Name),
		RedirectURL:               *form.GetString(r.Form, param.RedirectURL.Name),
		ActionCompleteRedirectURL: form.GetString(r.Form, param.ActionCompleteRedirectURL.Name),
		AdditionalScopes:          form.GetStringArray(r.Form, param.AdditionalScope.Name),
		Origin:                    r.Header.Get("Origin"),
		User:                      user,
	}

	resp, err := h.userService.ConnectOAuthAccount(ctx, createForm)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// PATCH /v1/me/external_accounts/{externalAccountID}/reauthorize
func (h *HTTP) ReauthorizeOAuthAccount(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	reqParams := param.NewSet(param.RedirectURL)
	optParams := param.NewSet(param.AdditionalScope, param.ActionCompleteRedirectURL)

	err := form.Check(r.Form, param.NewList(reqParams, optParams))
	if err != nil {
		return nil, err
	}

	resp, err := h.userService.ReauthorizeOAuthAccount(ctx, &reauthorizeOAuthAccountParams{
		AdditionalScopes:          form.GetStringArray(r.Form, param.AdditionalScope.Name),
		RedirectURL:               *form.GetString(r.Form, param.RedirectURL.Name),
		ActionCompleteRedirectURL: form.GetString(r.Form, param.ActionCompleteRedirectURL.Name),
		ID:                        chi.URLParam(r, "externalAccountID"),
		Origin:                    r.Header.Get("Origin"),
		User:                      user,
	})
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// DELETE /v1/me/external_accounts/{externalAccountID}
func (h *HTTP) DisconnectOAuthAccount(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	if formErrs := form.CheckEmpty(r.Form); formErrs != nil {
		return nil, formErrs
	}

	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)
	externalAccountID := chi.URLParam(r, "externalAccountID")

	externalAccount, err := h.userService.DeleteExternalAccount(ctx, user, externalAccountID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, externalAccount, client)
}

// POST /v1/me/totp
func (h *HTTP) CreateTOTP(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	resp, err := h.userService.CreateTOTP(ctx, user)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// POST /v1/me/totp/attempt_verification
func (h *HTTP) AttemptTOTPVerification(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	err := form.Check(r.Form, param.NewList(param.NewSet(param.Code), param.NewSet()))
	if err != nil {
		return nil, err
	}

	resp, err := h.userService.AttemptTOTPVerification(ctx, user, *form.GetString(r.Form, param.Code.Name))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// DELETE /v1/me/totp
func (h *HTTP) DeleteTOTP(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	resp, err := h.userService.DeleteTOTP(ctx, user)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

// POST /v1/me/backup_codes
func (h *HTTP) CreateBackupCodes(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	resp, err := h.userService.CreateBackupCodes(ctx, user)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, resp, client)
}

func requestParse(r *http.Request, key string) *string {
	headerValues, ok := r.Header[key]

	if ok {
		val := headerValues[0]
		return &val
	}

	snakeKey := strings.ToLower(strings.ReplaceAll(key, "-", "_"))

	prependedSnakeKey := "_" + snakeKey
	formValues, ok := r.Form[prependedSnakeKey]

	if ok {
		val := formValues[0]
		delete(r.Form, prependedSnakeKey)
		return &val
	}

	return nil
}

// ListOrganizationMemberships handles requests to GET /me/organization_memberships
func (h *HTTP) ListOrganizationMemberships(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	err := form.CheckWithPagination(r.Form, param.NewList(param.NewSet(), param.NewSet(param.Paginated)))
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	response, err := h.userService.ListOrganizationMemberships(ctx, ListOrganizationMembershipsParams{
		UserID:    user.ID,
		Paginated: form.GetBool(r.Form, param.Paginated.Name),
	}, paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// DELETE /v1/me/organization_memberships/{organizationID}
func (h *HTTP) DeleteOrganizationMembership(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, err
	}

	response, err := h.userService.DeleteOrganizationMembership(ctx, chi.URLParam(r, "organizationID"), user.ID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// GET /v1/me/organization_invitations
func (h *HTTP) ListOrganizationInvitations(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	err := form.CheckWithPagination(r.Form, param.NewList(param.NewSet(), param.NewSet(param.Status)))
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListOrganizationInvitationsParams{
		UserID:   user.ID,
		Statuses: form.GetStringArray(r.Form, param.Status.Name),
	}
	response, err := h.userService.ListOrganizationInvitations(ctx, params, paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// POST /v1/me/organization_invitations/{invitationID}/accept
func (h *HTTP) AcceptOrganizationInvitation(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)
	invitationID := chi.URLParam(r, "invitationID")

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	response, err := h.userService.AcceptOrganizationInvitation(ctx, invitationID, user.ID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// GET /v1/me/organization_suggestions
func (h *HTTP) ListOrganizationSuggestions(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)

	err := form.CheckWithPagination(r.Form, param.NewList(param.NewSet(), param.NewSet(param.Status)))
	if err != nil {
		return nil, err
	}

	paginationParams, err := pagination.NewFromRequest(r)
	if err != nil {
		return nil, err
	}

	params := ListOrganizationSuggestionsParams{
		UserID:   user.ID,
		Statuses: form.GetStringArray(r.Form, param.Status.Name),
	}
	response, err := h.userService.ListOrganizationSuggestions(ctx, params, paginationParams)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// POST /v1/me/organization_suggestions/{suggestionID}/accept
func (h *HTTP) AcceptOrganizationSuggestion(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	user := requesting_user.FromContext(ctx)
	suggestionID := chi.URLParam(r, "suggestionID")

	if err := form.CheckEmpty(r.Form); err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	response, err := h.userService.AcceptOrganizationSuggestion(ctx, suggestionID, user.ID)
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}

// PATCH /v1/me/password
func (h *HTTP) ChangePassword(_ http.ResponseWriter, r *http.Request) (interface{}, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	requestingUser := requesting_user.FromContext(ctx)

	reqParams := param.NewSet(param.NewPassword)
	optParams := param.NewSet(param.CurrentPassword, param.SignOutOfOtherSessions)

	err := form.Check(r.Form, param.NewList(reqParams, optParams))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	response, err := h.userService.ChangePassword(ctx, ChangePasswordParams{
		CurrentPassword:        form.GetString(r.Form, param.CurrentPassword.Name),
		NewPassword:            *form.GetString(r.Form, param.NewPassword.Name),
		SignOutOfOtherSessions: form.GetBool(r.Form, param.SignOutOfOtherSessions.Name),
		User:                   requestingUser,
	})
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	return h.wrapper.WrapResponse(ctx, response, client)
}

// DELETE /v1/me/password
func (h *HTTP) DeletePassword(_ http.ResponseWriter, r *http.Request) (any, apierror.Error) {
	ctx := r.Context()
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	requestingUser := requesting_user.FromContext(ctx)

	reqParams := param.NewSet(param.CurrentPassword)
	err := form.Check(r.Form, param.NewList(reqParams, param.NewSet()))
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}

	response, err := h.userService.DeletePassword(ctx, DeletePasswordParams{
		CurrentPassword: *form.GetString(r.Form, param.CurrentPassword.Name),
		User:            requestingUser,
	})
	if err != nil {
		return nil, h.wrapper.WrapError(ctx, err, client)
	}
	return h.wrapper.WrapResponse(ctx, response, client)
}
