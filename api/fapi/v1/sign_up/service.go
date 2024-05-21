package sign_up

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/clients"
	"clerk/api/shared/client_data"
	"clerk/api/shared/session_activities"
	"clerk/api/shared/sessions"
	"clerk/api/shared/sign_up"
	sharedstrategies "clerk/api/shared/strategies"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/activity"
	"clerk/pkg/ctx/client_type"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/externalapis/segment"
	"clerk/pkg/externalapis/turnstile"
	"clerk/pkg/metadata"
	"clerk/pkg/segment/fapi"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/pkg/usersettings/clerk/strategies"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	log "clerk/utils/log"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	// dependencies
	deps              clerk.Deps
	db                database.Database
	clock             clockwork.Clock
	captchaClientPool *turnstile.ClientPool

	// services
	clientService            *clients.Service
	clientDataService        *client_data.Service
	signUpService            *sign_up.Service
	verificationService      *verifications.Service
	sessionService           *sessions.Service
	sessionActivitiesService *session_activities.Service

	// repositories
	accountTransferRepo *repository.AccountTransfers
	externalAccountRepo *repository.ExternalAccount
	identificationRepo  *repository.Identification
	redirectUrlsRepo    *repository.RedirectUrls
	verificationRepo    *repository.Verification
	samlAccountRepo     *repository.SAMLAccount
	signUpRepo          *repository.SignUp
	signInRepo          *repository.SignIn
}

func NewService(deps clerk.Deps, captchaClientPool *turnstile.ClientPool) *Service {
	return &Service{
		deps:                     deps,
		db:                       deps.DB(),
		clock:                    deps.Clock(),
		captchaClientPool:        captchaClientPool,
		clientService:            clients.NewService(deps),
		clientDataService:        client_data.NewService(deps),
		signUpService:            sign_up.NewService(deps),
		verificationService:      verifications.NewService(deps.Clock()),
		sessionService:           sessions.NewService(deps),
		sessionActivitiesService: session_activities.NewService(),
		accountTransferRepo:      repository.NewAccountTransfers(),
		externalAccountRepo:      repository.NewExternalAccount(),
		identificationRepo:       repository.NewIdentification(),
		redirectUrlsRepo:         repository.NewRedirectUrls(),
		verificationRepo:         repository.NewVerification(),
		samlAccountRepo:          repository.NewSAMLAccount(),
		signUpRepo:               repository.NewSignUp(),
		signInRepo:               repository.NewSignIn(),
	}
}

// SetSignUp returns a sign up aware context. It retrieves the sign up
// object specified by signUpID and loads it into the context. A
// apierror.SignUpNotFound error will be returned if the sign up cannot
// be found, has been abandoned, or does not belong to the current client.
func (s *Service) SetSignUp(ctx context.Context, signUpID string) (context.Context, apierror.Error) {
	env := environment.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	signUp, err := s.signUpRepo.QueryByIDAndInstance(ctx, s.db, signUpID, env.Instance.ID)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	}

	if signUp == nil {
		return ctx, apierror.SignUpNotFound(signUpID)
	}
	if signUp.ClientID != client.ID {
		return ctx, apierror.SignUpNotFound(signUpID)
	}
	if signUp.Abandoned(s.clock) {
		return ctx, apierror.SignUpNotFound(signUpID)
	}

	return sign_up.NewSignUpAwareContext(ctx, signUp), nil
}

type SignUpForm struct {
	Transfer                  *bool
	Password                  *string
	FirstName                 *string
	LastName                  *string
	Username                  *string
	EmailAddress              *string
	PhoneNumber               *string
	EmailAddressOrPhoneNumber *string
	UnsafeMetadata            *[]byte
	Strategy                  *string
	RedirectURL               *string
	ActionCompleteRedirectURL *string
	Ticket                    *string
	Web3Wallet                *string
	Token                     *string
	Origin                    string
	CaptchaToken              *string
	CaptchaError              *string
	CaptchaWidgetType         *string
}

func (suf SignUpForm) toStrategiesSignUpPrepareForm(clientID string) strategies.SignUpPrepareForm {
	return strategies.SignUpPrepareForm{
		Strategy:                  *suf.Strategy,
		RedirectURL:               suf.RedirectURL,
		ActionCompleteRedirectURL: suf.ActionCompleteRedirectURL,
		Origin:                    suf.Origin,
		ClientID:                  clientID,
	}
}

// Create creates a new sign-up.
func (s *Service) Create(ctx context.Context, createForm *SignUpForm) (*model.SignUp, *model.Client, bool, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	deviceActivity := activity.FromContext(ctx)

	client, _ := s.clientService.GetClientFromContext(ctx)

	if env.Instance.IsDevelopment() {
		fapi.EnqueueSegmentEvent(ctx, s.deps.GueClient(), fapi.SegmentParams{EventName: segment.APIFrontendSignUpStarted})
	}

	// if in single_session_mode, can't sign_up if you are already signed in.
	if env.AuthConfig.SessionSettings.SingleSessionMode && client != nil {
		activeSessions, err := s.clientDataService.FindAllClientSessions(ctx, env.Instance.ID, client.ID, &client_data.SessionFilterParams{
			ActiveOnly: true,
		})
		if err != nil {
			return nil, nil, false, apierror.Unexpected(err)
		} else if len(activeSessions) > 0 {
			return nil, nil, false, apierror.SingleModeSessionExists()
		}
	}

	// Bot detection
	apiErr := s.handleCaptcha(
		ctx,
		createForm.CaptchaToken,
		createForm.CaptchaWidgetType,
		createForm.CaptchaError,
		createForm.Origin,
		env.Instance,
		userSettings.SignUp,
		client_type.FromContext(ctx),
	)
	if apiErr != nil {
		return nil, nil, false, apiErr
	}

	// create client and sign-up if needed, before anything else.
	tmpClient := client
	signUp, client, err := s.createSignUp(ctx, client, env.Instance, deviceActivity)
	if err != nil {
		return nil, nil, false, apierror.Unexpected(err)
	}

	newClientCreated := tmpClient != client

	var attemptor sharedstrategies.Attemptor
	var newSession *model.Session
	newSessionCreated := false
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// This transaction includes the following steps:
		// 1. Validate request: This includes validating the request parameters for their correctness as well as
		//    their applicability to the instance's user settings.
		// 2. Perform transfer flow: If the request is part of a transfer flow, make the necessary changes.
		// 3. Prepare/Attempt steps: If strategy is supplied, run either the prepare step (if strategy is
		//    preparable) or the attempt step (if strategy is attemptable).
		// 4. Finalize flow

		// 1. Validate request
		formErrors := validateRequestAndPopulateSignUp(ctx, s.deps, tx, env, userSettings, signUp, createForm)

		// validate and retrieve the given strategy, if there is one.
		strategy, apiErr := validateSignUpStrategy(userSettings, createForm)
		formErrors = apierror.Combine(formErrors, apiErr)

		if formErrors != nil {
			return true, formErrors
		}

		// 2. Perform transfer flow
		if createForm.Transfer != nil && *createForm.Transfer {
			// remove transfer obj. (consumed)
			accountTransferID, err := s.consumeSignUpTransfer(ctx, client)
			if err != nil {
				return true, err
			}
			if err := s.performSignUpTransfer(ctx, tx, env.Instance, client, signUp, accountTransferID); err != nil {
				return true, err
			}
		}

		if err := s.signUpRepo.Update(ctx, tx, signUp); err != nil {
			return true, err
		}

		// 3. Prepare/Attempt steps
		if preparable, ok := strategies.ToSignUpPreparable(strategy); ok {
			err := s.executeSignUpPreparableStrategy(ctx, tx, env, signUp, createForm.toStrategiesSignUpPrepareForm(client.ID), preparable)
			if err != nil {
				return true, err
			}
		} else if attemptable, ok := strategies.ToSignUpAttemptable(strategy); ok {
			attemptor, err = s.executeSignUpAttemptableStrategy(ctx, tx, env, signUp, createForm, attemptable)
			if err != nil {
				return true, err
			}
		}

		// In case there is a successful external account identification associated with the sign up
		// (OAuth/SAML transfer flow, SAML IdP-initiated flow via ticket), fetch the external or saml
		// account in order to populate the sign up with the corresponding data (email identification,
		// first/last name etc)
		var externalAccount *model.ExternalAccount
		var samlAccount *model.SAMLAccount
		if signUp.SuccessfulExternalAccountIdentificationID.Valid {
			identification, err := s.identificationRepo.FindByIDAndInstance(ctx, tx, signUp.SuccessfulExternalAccountIdentificationID.String, signUp.InstanceID)
			if err != nil {
				return true, err
			}

			if identification.IsOAuth() {
				externalAccount, err = s.externalAccountRepo.FindByIDAndInstance(ctx, tx, identification.ExternalAccountID.String, signUp.InstanceID)
				if err != nil {
					return true, err
				}
			} else if identification.IsSAML() {
				samlAccount, err = s.samlAccountRepo.FindByIDAndInstance(ctx, tx, identification.SamlAccountID.String, signUp.InstanceID)
				if err != nil {
					return true, err
				}
			} else {
				return true, fmt.Errorf("no account associated with transfer identification with id %s", identification.ID)
			}
		}

		// 4. Finalize flow
		newSession, err = s.signUpService.FinalizeFlow(
			ctx,
			tx,
			sign_up.FinalizeFlowParams{
				SignUp:               signUp,
				Env:                  env,
				Client:               client,
				ExternalAccount:      externalAccount,
				SAMLAccount:          samlAccount,
				UserSettings:         usersettings.NewUserSettings(env.AuthConfig.UserSettings),
				PostponeCookieUpdate: false,
			},
		)
		if errors.Is(err, clerkerrors.ErrIdentificationClaimed) {
			// Kill this sign-up, because another user claimed one of its identifications.
			// Promote error, and don't rollback since we nuked the sign-up.
			if err := s.resetClientSignup(ctx, env.Instance, client); err != nil {
				return false, err
			}
			return false, apierror.IdentificationClaimed()
		} else if err != nil {
			return true, err
		}

		newSessionCreated = newSession != nil
		return false, nil
	})
	if txErr != nil {
		var oauthErr clerkerrors.OAuthConfigMissing

		if errors.As(txErr, &oauthErr) {
			return nil, nil, false, apierror.OAuthConfigMissing(oauthErr.Provider)
		} else if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			// Reset the signup associated with the client on specific failures
			if isUniqueIdentificationError(apiErr) {
				if err := s.resetClientSignup(ctx, env.Instance, client); err != nil {
					return nil, nil, false, apierror.Unexpected(err)
				}
			}

			return signUp, client, newClientCreated, apiErr
		} else if errors.Is(txErr, sharedstrategies.ErrFailedExchangeCredentialsOAuth1) {
			return nil, nil, false, apierror.MisconfiguredOAuthProvider()
		} else if errors.Is(txErr, sharedstrategies.ErrSharedCredentialsNotAvailable) {
			return nil, nil, false, apierror.OAuthSharedCredentialsNotSupported()
		} else if errors.Is(txErr, sharedstrategies.ErrInvalidRedirectURL) {
			return nil, nil, false, apierror.InvalidRedirectURL()
		} else if attemptor != nil {
			return nil, nil, false, attemptor.ToAPIError(txErr)
		} else if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueReservedIdentification) {
			return nil, nil, false, apierror.IdentificationClaimed()
		}

		return signUp, client, newClientCreated, apierror.Unexpected(txErr)
	}

	if env.Instance.IsDevelopment() {
		fapi.EnqueueSegmentEvent(ctx, s.deps.GueClient(), fapi.SegmentParams{EventName: segment.APIFrontendUserCreated})
		if newSessionCreated {
			fapi.EnqueueSegmentEvent(ctx, s.deps.GueClient(), fapi.SegmentParams{EventName: segment.APIFrontendSessionCreated})
		}
	}

	if newSessionCreated && newSession != nil {
		if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
			return nil, nil, false, apierror.Unexpected(err)
		}
	}

	return signUp, client, newClientCreated || newSessionCreated, nil
}

func validateRequestAndPopulateSignUp(
	ctx context.Context,
	deps clerk.Deps,
	tx database.Tx,
	env *model.Env,
	userSettings *usersettings.UserSettings,
	signUp *model.SignUp,
	createOrUpdateForm *SignUpForm) apierror.Error {
	formErrors := convertEmailAddressOrPhoneNumber(userSettings, createOrUpdateForm)

	signUpForm := usersettings.SignUpForm{
		EmailAddress: createOrUpdateForm.EmailAddress,
		PhoneNumber:  createOrUpdateForm.PhoneNumber,
		Web3Wallet:   createOrUpdateForm.Web3Wallet,
		Username:     createOrUpdateForm.Username,
		Password:     createOrUpdateForm.Password,
		FirstName:    createOrUpdateForm.FirstName,
		LastName:     createOrUpdateForm.LastName,
	}

	// iterate through all attributes that can be used during sign up,
	// and add them to sign up (i.e. run sanitization, validation,
	// and populate sign up).
	for _, attribute := range userSettings.AllAttributes() {
		signUpAttribute, ok := usersettings.ToSignUpAttribute(attribute)
		if !ok {
			continue
		}
		apiErr := signUpAttribute.AddToSignUp(ctx, deps, tx, env, signUpForm, signUp)
		formErrors = apierror.Combine(formErrors, apiErr)
	}

	// validate all other properties which are not
	// user setting attributes, e.g. unsafe metadata
	apiErr := validateAndUpdateNonAttributeProperties(createOrUpdateForm, signUp)
	formErrors = apierror.Combine(formErrors, apiErr)

	return formErrors
}

// validateAndUpdateNonAttributeProperties will validate all those fields which are not attributes
// in user settings. For each of these, if it doesn't have any errors, it will be added to the sign up.
func validateAndUpdateNonAttributeProperties(signUpForm *SignUpForm, signUp *model.SignUp) apierror.Error {
	var formErrors apierror.Error
	if signUpForm.Transfer != nil {
		if !*signUpForm.Transfer {
			formErrors = apierror.Combine(formErrors, apierror.FormInvalidParameterValue(param.Transfer.Name, "false"))
		}
	}

	if signUpForm.UnsafeMetadata != nil {
		metadataError := metadata.Validate(metadata.Metadata{Unsafe: *signUpForm.UnsafeMetadata})
		formErrors = apierror.Combine(formErrors, metadataError)
		if metadataError == nil {
			signUp.UnsafeMetadata = *signUpForm.UnsafeMetadata
		}
	}

	return formErrors
}

// convertEmailAddressOrPhoneNumber is responsible for assigning the value of
// `email_address_or_phone_number` parameter (if it exists) into either the
// `email_address` parameter or the `phone_number` one.
// This simplifies the processing down the line.
func convertEmailAddressOrPhoneNumber(userSettings *usersettings.UserSettings, createForm *SignUpForm) apierror.Error {
	var formErrors apierror.Error
	if createForm.EmailAddressOrPhoneNumber != nil {
		// email_address_or_phone_number is only allowed if both email address and
		// phone number are enabled in the instance
		if !userSettings.GetAttribute(names.EmailAddress).Base().UsedForFirstFactor || !userSettings.GetAttribute(names.PhoneNumber).Base().UsedForFirstFactor {
			return apierror.FormUnknownParameter(param.EmailAddressOrPhoneNumber.Name)
		}

		if *createForm.EmailAddressOrPhoneNumber == "" {
			return apierror.FormNilParameter(param.EmailAddressOrPhoneNumber.Name)
		}

		// email_address_or_phone_number is not allowed when either email_address or phone_number is supplied
		if createForm.EmailAddress != nil {
			formErrors = apierror.Combine(formErrors, apierror.FormParameterNotAllowedConditionally(
				param.EmailAddressOrPhoneNumber.Name, param.EmailAddress.Name, "present"))
		}
		if createForm.PhoneNumber != nil {
			formErrors = apierror.Combine(formErrors, apierror.FormParameterNotAllowedConditionally(
				param.EmailAddressOrPhoneNumber.Name, param.PhoneNumber.Name, "present"))
		}

		if createForm.EmailAddress == nil && createForm.PhoneNumber == nil {
			// parse the value of email_address_or_phone_number and assign it to the respective field
			identificationType, err := detectEmailAddressOrPhoneNumber(*createForm.EmailAddressOrPhoneNumber)
			if err != nil {
				formErrors = apierror.Combine(formErrors, err)
			} else if identificationType == constants.ITEmailAddress {
				createForm.EmailAddress = createForm.EmailAddressOrPhoneNumber
			} else {
				createForm.PhoneNumber = createForm.EmailAddressOrPhoneNumber
			}
		}
	}

	if emailAddressOrPhoneNumber(userSettings) {
		emailProvided := createForm.EmailAddress != nil && *createForm.EmailAddress != ""
		phoneProvided := createForm.PhoneNumber != nil && *createForm.PhoneNumber != ""

		if createForm.EmailAddress != nil && *createForm.EmailAddress == "" && !phoneProvided {
			formErrors = apierror.Combine(formErrors, apierror.FormNilParameter(param.EmailAddress.Name))
		}
		if createForm.PhoneNumber != nil && *createForm.PhoneNumber == "" && !emailProvided {
			formErrors = apierror.Combine(formErrors, apierror.FormNilParameter(param.PhoneNumber.Name))
		}
	}

	return formErrors
}

func emailAddressOrPhoneNumber(userSettings *usersettings.UserSettings) bool {
	emailAddress := userSettings.GetAttribute(names.EmailAddress).Base()
	phoneNumber := userSettings.GetAttribute(names.PhoneNumber).Base()

	if userSettings.SignUp.Progressive {
		return emailAddress.Enabled && phoneNumber.Enabled && !emailAddress.Required && !phoneNumber.Required
	}

	return emailAddress.UsedForFirstFactor && phoneNumber.UsedForFirstFactor
}

// validateSignUpStrategy checks whether the given strategy (if any) can be used during
// sign up.
func validateSignUpStrategy(userSettings *usersettings.UserSettings, signUpForm *SignUpForm) (strategies.Strategy, apierror.Error) {
	if signUpForm.Strategy == nil {
		return nil, nil
	}

	// sign-up strategies = verification strategies + authenticatable social strategies
	signUpstrategies := userSettings.VerificationStrategies()
	signUpstrategies.Union(userSettings.AuthenticatableSocialStrategies())

	if !signUpstrategies.Contains(*signUpForm.Strategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, *signUpForm.Strategy)
	}

	strategy, strategyExists := strategies.GetStrategy(*signUpForm.Strategy)
	if !strategyExists {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, *signUpForm.Strategy)
	}

	return strategy, nil
}

func (s *Service) executeSignUpPreparableStrategy(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	signUp *model.SignUp,
	prepareForm strategies.SignUpPrepareForm,
	strategy strategies.SignUpPreparable) error {
	preparer, apiErr := strategy.CreateSignUpPreparer(ctx, tx, s.deps, env, signUp, prepareForm)
	if apiErr != nil {
		return apiErr
	}

	verification, err := preparer.Prepare(ctx, tx)
	if err != nil {
		return err
	}

	if verification.External() {
		signUp.ExternalAccountVerificationID = null.StringFrom(verification.ID)
		return s.signUpRepo.UpdateExternalAccountVerificationID(ctx, tx, signUp)
	}

	identification := preparer.Identification()

	if identification.IsVerified() {
		return apierror.VerificationAlreadyVerified()
	}

	identification.VerificationID = null.StringFrom(verification.ID)
	return s.identificationRepo.UpdateVerificationID(ctx, tx, identification)
}

func (s *Service) executeSignUpAttemptableStrategy(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	signUp *model.SignUp,
	createOrUpdateForm *SignUpForm,
	strategy strategies.SignUpAttemptable) (sharedstrategies.Attemptor, error) {
	attemptForm := strategies.SignUpAttemptForm{
		Strategy: *createOrUpdateForm.Strategy,
		Ticket:   createOrUpdateForm.Ticket,
		Token:    createOrUpdateForm.Token,
	}
	attemptor, apiErr := strategy.CreateSignUpAttemptor(ctx, tx, s.deps, env, signUp, attemptForm)
	if apiErr != nil {
		return attemptor, apiErr
	}

	verification, err := attemptor.Attempt(ctx, tx)
	if err != nil {
		return attemptor, err
	}

	if verification.External() {
		signUp.ExternalAccountVerificationID = null.StringFrom(verification.ID)
		if err := s.signUpRepo.UpdateExternalAccountVerificationID(ctx, tx, signUp); err != nil {
			return attemptor, err
		}
	}

	return attemptor, nil
}

// createSignUp creates a new sign-up for the given client in the given instance.
// If the current client does not exist, then it will also create a new client and return it.
func (s *Service) createSignUp(
	ctx context.Context,
	client *model.Client,
	instance *model.Instance,
	deviceActivity *model.SessionActivity) (*model.SignUp, *model.Client, error) {
	var signUp *model.SignUp
	if client == nil {
		var devBrowserID *string
		if dvb := requestingdevbrowser.FromContext(ctx); dvb != nil {
			devBrowserID = &dvb.ID
		}

		newClient, err := s.clientService.CreateWithDevBrowser(ctx, instance, devBrowserID)
		if err != nil {
			return nil, nil, fmt.Errorf("sign-up/create: create new client with devbrowser %v: %w", devBrowserID, err)
		}
		client = newClient
	}
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.sessionActivitiesService.CreateSessionActivity(ctx, tx, instance.ID, deviceActivity)
		if err != nil {
			return true, err
		}

		newSignUp := &model.SignUp{SignUp: &sqbmodel.SignUp{
			InstanceID:        instance.ID,
			ClientID:          client.ID,
			AuthConfigID:      instance.ActiveAuthConfigID,
			SessionActivityID: null.StringFrom(deviceActivity.ID),
			AbandonAt:         s.clock.Now().UTC().Add(time.Second * time.Duration(constants.ExpiryTimeMediumShort)),
		}}
		err = s.signUpRepo.Insert(ctx, tx, newSignUp)
		if err != nil {
			return true, fmt.Errorf("sign-up/create: insert new sign-up %+v: %w", newSignUp, err)
		}

		signUp = newSignUp
		return false, nil
	})
	if txErr != nil {
		return nil, nil, txErr
	}

	// Update client's SignUp id.
	client.SignUpID = null.StringFrom(signUp.ID)
	cdsClient := client_data.NewClientFromClientModel(client)
	if err := s.clientDataService.UpdateClientSignUpID(ctx, instance.ID, cdsClient); err != nil {
		return nil, nil, fmt.Errorf("sign-up/create: update sign-up id on client %s: %w", client, err)
	}
	cdsClient.CopyToClientModel(client)

	return signUp, client, nil
}

func detectEmailAddressOrPhoneNumber(identifier string) (string, apierror.Error) {
	if err := validate.EmailAddress(identifier, param.EmailAddress.Name); err == nil {
		return constants.ITEmailAddress, nil
	}
	if err := validate.PhoneNumber(identifier, param.PhoneNumber.Name); err == nil {
		return constants.ITPhoneNumber, nil
	}
	return "", apierror.FormInvalidParameterFormat(param.EmailAddressOrPhoneNumber.Name)
}

func isUniqueIdentificationError(apiErr apierror.Error) bool {
	return apierror.CauseMatches(apiErr, func(cause error) bool {
		return clerkerrors.IsUniqueConstraintViolation(cause, clerkerrors.UniqueIdentification)
	})
}

// Update updates an existing sign up
func (s *Service) Update(ctx context.Context, updateForm *SignUpForm) (*model.SignUp, *model.Client, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	signUp := sign_up.FromContext(ctx)

	var attemptor sharedstrategies.Attemptor
	var newSession *model.Session
	var newSessionCreated bool
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		formErrors := validateRequestAndPopulateSignUp(ctx, s.deps, tx, env, userSettings, signUp, updateForm)

		// validate and retrieve the given strategy, if there is one.
		strategy, apiErr := validateSignUpStrategy(userSettings, updateForm)
		formErrors = apierror.Combine(formErrors, apiErr)

		if formErrors != nil {
			return true, formErrors
		}

		if err := s.signUpRepo.Update(ctx, tx, signUp); err != nil {
			return true, err
		}

		if preparable, ok := strategies.ToSignUpPreparable(strategy); ok {
			err := s.executeSignUpPreparableStrategy(ctx, tx, env, signUp, updateForm.toStrategiesSignUpPrepareForm(client.ID), preparable)
			if err != nil {
				return true, err
			}
		} else if attemptable, ok := strategies.ToSignUpAttemptable(strategy); ok {
			var err error
			attemptor, err = s.executeSignUpAttemptableStrategy(ctx, tx, env, signUp, updateForm, attemptable)
			if err != nil {
				return true, err
			}
		}

		var externalAccount *model.ExternalAccount
		// Sign up has an external account verification. Fetch the external account
		// so we can fill in all user info from the oauth provider later when we
		// finalize the sign up.
		if signUp.SuccessfulExternalAccountIdentificationID.Valid {
			var err error
			externalAccount, err = s.externalAccountRepo.QueryByIdentificationID(ctx, tx, signUp.SuccessfulExternalAccountIdentificationID.String)
			if err != nil {
				return true, err
			}
		}

		var err error
		newSession, err = s.signUpService.FinalizeFlow(
			ctx,
			tx,
			sign_up.FinalizeFlowParams{
				SignUp:               signUp,
				Env:                  env,
				Client:               client,
				UserSettings:         usersettings.NewUserSettings(env.AuthConfig.UserSettings),
				PostponeCookieUpdate: false,
				ExternalAccount:      externalAccount,
			},
		)
		if errors.Is(err, clerkerrors.ErrIdentificationClaimed) {
			// Kill this sign-up, because another user claimed one of its identifications.
			// Promote error, and don't rollback since we nuked the sign-up.
			if err := s.resetClientSignup(ctx, env.Instance, client); err != nil {
				return false, err
			}
			return false, apierror.IdentificationClaimed()
		} else if err != nil {
			return true, err
		}

		newSessionCreated = newSession != nil
		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			if isUniqueIdentificationError(apiErr) {
				if err := s.resetClientSignup(ctx, env.Instance, client); err != nil {
					return nil, nil, apierror.Unexpected(err)
				}
			}
			return signUp, nil, apiErr
		} else if attemptor != nil {
			return nil, nil, attemptor.ToAPIError(txErr)
		} else if errors.Is(txErr, sharedstrategies.ErrInvalidRedirectURL) {
			return nil, nil, apierror.InvalidRedirectURL()
		} else if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueReservedIdentification) {
			return nil, nil, apierror.IdentificationClaimed()
		}
		return nil, nil, apierror.Unexpected(txErr)
	}

	if newSessionCreated {
		if newSession != nil {
			if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
				return nil, nil, apierror.Unexpected(err)
			}
		}
		return signUp, client, nil
	}

	return signUp, nil, nil
}

func (s *Service) consumeSignUpTransfer(ctx context.Context, client *model.Client) (null.String, error) {
	accountTransferID := client.ToSignUpAccountTransferID
	client.ToSignUpAccountTransferID = null.StringFromPtr(nil)
	cdsClient := client_data.NewClientFromClientModel(client)
	if err := s.clientDataService.UpdateClientToSignUpAccountTransferID(ctx, cdsClient); err != nil {
		return null.StringFromPtr(nil), err
	}
	cdsClient.CopyToClientModel(client)
	return accountTransferID, nil
}

func (s *Service) performSignUpTransfer(
	ctx context.Context,
	tx database.Tx,
	instance *model.Instance,
	client *model.Client,
	signUp *model.SignUp,
	accountTransferID null.String,
) apierror.Error {
	accTransferID := accountTransferID.Ptr()
	if accTransferID == nil {
		return apierror.AccountTransferInvalid()
	}

	accTransfer, err := s.accountTransferRepo.QueryByInstanceAndID(ctx, tx, instance.ID, *accTransferID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if accTransfer == nil {
		return apierror.AccountTransferInvalid()
	}

	// clear the verification on the sign_in
	ver, err := s.verificationService.Clear(ctx, tx, accTransfer.ID)
	if err != nil {
		if errors.Is(err, verifications.ErrVerificationNotFound) {
			return apierror.AccountTransferInvalid()
		}
		return apierror.Unexpected(err)
	}

	// reset the sign_in to identification
	if client.SignInID.Valid {
		signIn, err := s.signInRepo.FindByIDAndInstance(ctx, tx, client.SignInID.String, instance.ID)
		if err != nil {
			return apierror.Unexpected(err)
		}

		if err := s.resetSignInClientIdentification(ctx, tx, signIn); err != nil {
			return apierror.Unexpected(err)
		}
	}

	// add identification and verification to sign-up
	//
	// Note: we want to keep track of the verification created by this user.
	// If this user ends up resolving the identification,
	// we will attach this verification to it.
	signUp.SuccessfulExternalAccountIdentificationID = null.StringFrom(accTransfer.IdentificationID)
	signUp.ExternalAccountVerificationID = null.StringFrom(ver.ID)
	err = s.signUpRepo.Update(ctx, tx, signUp, sqbmodel.SignUpColumns.SuccessfulExternalAccountIdentificationID, sqbmodel.SignUpColumns.ExternalAccountVerificationID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}

// reset the sign in attempt's client to unidentified
func (s *Service) resetSignInClientIdentification(
	ctx context.Context, tx database.Tx,
	signIn *model.SignIn) error {
	whitelistColumns := []string{
		sqbmodel.SignInColumns.IdentificationID,
		sqbmodel.SignInColumns.FirstFactorCurrentVerificationID,
		sqbmodel.SignInColumns.FirstFactorSuccessVerificationID,
		sqbmodel.SignInColumns.SecondFactorCurrentVerificationID,
	}

	signIn.IdentificationID = null.StringFromPtr(nil)
	signIn.FirstFactorCurrentVerificationID = null.StringFromPtr(nil)
	signIn.FirstFactorSuccessVerificationID = null.StringFromPtr(nil)
	signIn.SecondFactorCurrentVerificationID = null.StringFromPtr(nil)

	return s.signInRepo.Update(ctx, tx, signIn, whitelistColumns...)
}

type SignUpPrepareForm struct {
	Strategy                  string
	RedirectURL               *string
	ActionCompleteRedirectURL *string
	Origin                    string
}

func (suf SignUpPrepareForm) toStrategiesSignUpPrepareForm(clientID string) strategies.SignUpPrepareForm {
	return strategies.SignUpPrepareForm{
		Strategy:                  suf.Strategy,
		RedirectURL:               suf.RedirectURL,
		ActionCompleteRedirectURL: suf.ActionCompleteRedirectURL,
		Origin:                    suf.Origin,
		ClientID:                  clientID,
	}
}

// PrepareVerification sets up the verification for the given strategy for the current sign-up.
func (s *Service) PrepareVerification(ctx context.Context, prepareForm *SignUpPrepareForm) (*model.SignUp, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	signUp := sign_up.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	strategy, apiErr := validatePreparableSignUpStrategy(userSettings, prepareForm.Strategy)
	if apiErr != nil {
		return nil, apiErr
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.executeSignUpPreparableStrategy(ctx, tx, env, signUp, prepareForm.toStrategiesSignUpPrepareForm(client.ID), strategy)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if knownErr, ok := apierror.As(txErr); ok {
			return nil, knownErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return signUp, nil
}

// validatePreparableSignUpStrategy checks whether the given strategy (if any) can be used during sign up.
func validatePreparableSignUpStrategy(userSettings *usersettings.UserSettings, strategy string) (strategies.SignUpPreparable, apierror.Error) {
	// prepare verification strategies = verification strategies + authenticatable social strategies
	prepareVerificationStrategies := userSettings.VerificationStrategies()
	prepareVerificationStrategies.Union(userSettings.AuthenticatableSocialStrategies())

	if !prepareVerificationStrategies.Contains(strategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, strategy)
	}

	selectedStrategy, strategyExists := strategies.GetStrategy(strategy)
	if !strategyExists || !strategies.IsPreparableDuringSignUp(selectedStrategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, strategy)
	}

	return selectedStrategy.(strategies.SignUpPreparable), nil
}

// AttemptVerification attempts to verify the corresponding identification of the current sign-up,
// using the given verification code.
func (s *Service) AttemptVerification(ctx context.Context, attemptForm strategies.SignUpAttemptForm) (*model.SignUp, bool, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	signUp := sign_up.FromContext(ctx)

	// oauth strategies are not applicable to attempt verification step, check only verification strategies
	attemptVerificationStrategies := userSettings.VerificationStrategies()

	if !attemptVerificationStrategies.Contains(attemptForm.Strategy) {
		return nil, false, apierror.FormInvalidParameterValue(param.Strategy.Name, attemptForm.Strategy)
	}

	selectedStrategy, strategyExists := strategies.GetStrategy(attemptForm.Strategy)
	if !strategyExists || !strategies.IsAttemptableDuringSignUp(selectedStrategy) {
		return nil, false, apierror.FormInvalidParameterValue(param.Strategy.Name, attemptForm.Strategy)
	}
	strategy := selectedStrategy.(strategies.SignUpAttemptable)

	var newSession *model.Session
	newSessionCreated := false
	var attemptor sharedstrategies.Attemptor
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// Fetch attemptor for given strategy
		var apiErr apierror.Error
		attemptor, apiErr = strategy.CreateSignUpAttemptor(ctx, tx, s.deps, env, signUp, attemptForm)
		if apiErr != nil {
			return true, apiErr
		}

		// Attempt to verify the identification
		verification, err := sharedstrategies.AttemptVerification(ctx, tx, attemptor, s.verificationRepo, client.ID)
		if errors.Is(err, sharedstrategies.ErrInvalidCode) {
			return false, err
		} else if err != nil {
			return true, err
		}

		identification, err := s.identificationRepo.QueryByVerificationID(ctx, tx, verification.ID)
		if err != nil {
			return true, err
		}

		// Handle the case where a parallel request to prepare was made while this attempt is still
		// processing, in which case the verification id on the identification would be overwritten
		// and the identification would not be able to load.
		if identification == nil {
			return true, apierror.SignUpOutdatedVerification()
		}

		// Mark identification as verified.
		identification.Status = constants.ISVerified
		if err := s.identificationRepo.UpdateStatus(ctx, tx, identification); err != nil {
			return true, err
		}

		var externalAccount *model.ExternalAccount
		// Sign up has an external account verification. Fetch the external account so we can fill in all user info
		// from the oauth provider later when we finalize the sign up.
		if signUp.SuccessfulExternalAccountIdentificationID.Valid {
			externalAccount, err = s.externalAccountRepo.QueryByIdentificationID(ctx, tx, signUp.SuccessfulExternalAccountIdentificationID.String)
			if err != nil {
				return true, err
			}
		}

		// Finalize sign up flow
		newSession, err = s.signUpService.FinalizeFlow(
			ctx,
			tx,
			sign_up.FinalizeFlowParams{
				SignUp:               signUp,
				Env:                  env,
				Client:               client,
				UserSettings:         usersettings.NewUserSettings(env.AuthConfig.UserSettings),
				PostponeCookieUpdate: false,
				ExternalAccount:      externalAccount,
			},
		)
		if errors.Is(err, clerkerrors.ErrIdentificationClaimed) {
			// Kill this sign-up, because another user claimed one of its identifications.
			// Promote error, and don't rollback since we nuked the sign-up.
			if err := s.resetClientSignup(ctx, env.Instance, client); err != nil {
				return true, apierror.Unexpected(err)
			}
			return false, apierror.IdentificationClaimed()
		} else if err != nil {
			return true, err
		}
		newSessionCreated = newSession != nil
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			if isUniqueIdentificationError(apiErr) {
				if err := s.resetClientSignup(ctx, env.Instance, client); err != nil {
					return nil, false, apierror.Unexpected(err)
				}
			}
			return nil, false, apiErr
		} else if attemptor != nil {
			return nil, false, attemptor.ToAPIError(txErr)
		} else if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueReservedIdentification) {
			return nil, false, apierror.IdentificationClaimed()
		}

		return nil, false, apierror.Unexpected(txErr)
	}

	if newSessionCreated && newSession != nil {
		if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
			return nil, false, apierror.Unexpected(err)
		}
	}

	return signUp, newSessionCreated, nil
}

func (s *Service) handleCaptcha(
	ctx context.Context,
	token, widgetType *string,
	captchaClientSideError *string,
	origin string,
	instance *model.Instance,
	settings usersettingsmodel.SignUp,
	clientType client_type.ClientType,
) apierror.Error {
	if !settings.CaptchaEnabled {
		if token != nil {
			return apierror.CaptchaNotEnabled()
		}
		return nil
	}

	if !instance.IsProduction() {
		return apierror.CaptchaNotEnabled()
	}

	if clientType.IsSet() && !clientType.IsBrowser() {
		return apierror.CaptchaUnsupportedByClient(instance.Communication.SupportEmail.Ptr())
	}

	logWarning := func(msg string) {
		log.Warning(ctx, fmt.Errorf("captcha-error: %s", msg))
	}

	if captchaClientSideError != nil {
		// there was an error returned by the Turnstile service client-side, and
		// Clerk.js relayed it to us.
		logWarning(*captchaClientSideError)
		return apierror.CaptchaInvalid()
	}

	if token == nil || *token == "" {
		logWarning("missing token")
		return apierror.CaptchaInvalid()
	}

	u, err := url.ParseRequestURI(origin)
	if err != nil {
		logWarning("invalid origin: " + origin)
		return apierror.CaptchaInvalid()
	}

	wt := settings.CaptchaWidgetType
	widgetTypeParamPresent := widgetType != nil && constants.TurnstileWidgetTypes.Contains(constants.TurnstileWidgetType(*widgetType))
	if widgetTypeParamPresent {
		wt = constants.TurnstileWidgetType(*widgetType)
	}

	ok, err := s.captchaClientPool.VerifyWithFallback(ctx, u.Host, *token, wt, !widgetTypeParamPresent)
	if err != nil {
		logWarning(err.Error())
		return nil // fail open
	}
	if ok {
		return nil
	}

	return apierror.CaptchaInvalid()
}

func (s *Service) resetClientSignup(ctx context.Context, instance *model.Instance, client *model.Client) error {
	client.SignUpID = null.StringFromPtr(nil)
	cdsClient := client_data.NewClientFromClientModel(client)
	if err := s.clientDataService.UpdateClientSignUpID(ctx, instance.ID, cdsClient); err != nil {
		return err
	}
	cdsClient.CopyToClientModel(client)
	return nil
}
