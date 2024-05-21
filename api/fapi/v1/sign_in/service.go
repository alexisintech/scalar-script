package sign_in

import (
	"context"
	"errors"
	"fmt"
	"time"

	"clerk/api/apierror"
	"clerk/api/fapi/v1/clients"
	"clerk/api/shared/client_data"
	"clerk/api/shared/restrictions"
	"clerk/api/shared/saml"
	"clerk/api/shared/session_activities"
	"clerk/api/shared/sessions"
	"clerk/api/shared/sign_in"
	sharedstrategies "clerk/api/shared/strategies"
	userlockout "clerk/api/shared/user_lockout"
	"clerk/api/shared/users"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/backup_codes"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/activity"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/externalapis/segment"
	"clerk/pkg/hash"
	"clerk/pkg/segment/fapi"
	"clerk/pkg/set"
	cstrings "clerk/pkg/strings"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/strategies"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	deps clerk.Deps

	// services
	clientService            *clients.Service
	clientDataService        *client_data.Service
	restrictionService       *restrictions.Service
	signInService            *sign_in.Service
	userLockoutService       *userlockout.Service
	userService              *users.Service
	verificationService      *verifications.Service
	sessionService           *sessions.Service
	sessionActivitiesService *session_activities.Service

	// repositories
	accountTransferRepo *repository.AccountTransfers
	identificationRepo  *repository.Identification
	signInRepo          *repository.SignIn
	userRepo            *repository.Users
	verificationRepo    *repository.Verification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		deps:                     deps,
		restrictionService:       restrictions.NewService(deps.EmailQualityChecker()),
		clientService:            clients.NewService(deps),
		clientDataService:        client_data.NewService(deps),
		signInService:            sign_in.NewService(deps),
		userLockoutService:       userlockout.NewService(deps),
		userService:              users.NewService(deps),
		verificationService:      verifications.NewService(deps.Clock()),
		sessionService:           sessions.NewService(deps),
		sessionActivitiesService: session_activities.NewService(),
		accountTransferRepo:      repository.NewAccountTransfers(),
		identificationRepo:       repository.NewIdentification(),
		signInRepo:               repository.NewSignIn(),
		userRepo:                 repository.NewUsers(),
		verificationRepo:         repository.NewVerification(),
	}
}

var (
	resetPasswordStrategies = set.New(constants.VSResetPasswordEmailCode, constants.VSResetPasswordPhoneCode)
)

// SetSignIn finds the given sign-in attempt and adds it to the request's context
func (s *Service) SetSignIn(ctx context.Context, signInID string) (context.Context, apierror.Error) {
	env := environment.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	signIn, err := s.signInRepo.QueryByIDAndInstance(ctx, s.deps.DB(), signInID, env.Instance.ID)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	} else if signIn == nil {
		return ctx, apierror.SignInNotFound(signInID)
	}

	if signIn.ClientID != client.ID {
		return ctx, apierror.SignInNotFound(signInID)
	}

	if signIn.Abandoned(s.deps.Clock()) {
		return ctx, apierror.InvalidClientStateForAction("a get", "No sign_in.")
	}

	return context.WithValue(ctx, ctxkeys.SignIn, signIn), nil
}

// EnsureLatestClientSignIn makes sure that the given sign in id is the latest one of the given client
func (s *Service) EnsureLatestClientSignIn(ctx context.Context) apierror.Error {
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	signIn := ctx.Value(ctxkeys.SignIn).(*model.SignIn)

	if !client.SignInID.Valid {
		return apierror.InvalidClientStateForAction("a get", "No sign_in.")
	}

	if client.SignInID.String != signIn.ID {
		return apierror.MutationOnOlderSignInNotAllowed()
	}

	return nil
}

type SignInCreateForm struct {
	Transfer                  *bool
	Identifier                *string
	Strategy                  *string
	RedirectURL               *string
	ActionCompleteRedirectURL *string
	Password                  *string
	Ticket                    *string
	Token                     *string
	Origin                    string
}

func (form SignInCreateForm) ToPrepareForm(identification *model.Identification, clientID string) strategies.SignInPrepareForm {
	prepareForm := strategies.SignInPrepareForm{
		Strategy:                  *form.Strategy,
		RedirectURL:               form.RedirectURL,
		ActionCompleteRedirectURL: form.ActionCompleteRedirectURL,
		Origin:                    form.Origin,
		ClientID:                  clientID,
	}

	if identification != nil {
		switch identification.Type {
		case constants.ITEmailAddress:
			prepareForm.EmailAddressID = &identification.ID
		case constants.ITPhoneNumber:
			prepareForm.PhoneNumberID = &identification.ID
		case constants.ITWeb3Wallet:
			prepareForm.Web3WalletID = &identification.ID
		}
	}

	return prepareForm
}

func (form SignInCreateForm) ToAttemptForm() strategies.SignInAttemptForm {
	return strategies.SignInAttemptForm{
		Strategy: *form.Strategy,
		Password: form.Password,
		Ticket:   form.Ticket,
		Token:    form.Token,
	}
}

// Create creates a new sign-in attempt on the client
func (s *Service) Create(ctx context.Context, signInForm SignInCreateForm) (*model.SignIn, *model.Client, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	deviceActivity := activity.FromContext(ctx)

	if signInForm.Strategy != nil &&
		*signInForm.Strategy == constants.VSPasskey &&
		!cenv.ResourceHasAccess(cenv.FlagAllowPasskeysInstanceIDs, env.Instance.ID) {
		return nil, nil, apierror.FeatureNotEnabled()
	}

	currentClient, _ := s.clientService.GetClientFromContext(ctx)

	if env.Instance.IsDevelopment() {
		fapi.EnqueueSegmentEvent(ctx, s.deps.GueClient(), fapi.SegmentParams{EventName: segment.APIFrontendSignInStarted})
	}

	// For transfer requests, validate transfer parameter value
	if signInForm.Transfer != nil && !*signInForm.Transfer {
		return nil, nil, apierror.FormInvalidParameterValue(param.Transfer.Name, "false")
	}

	// If single session instance, can't sign_in if you already are.
	if currentClient != nil {
		// Check if the current client contains any active sessions and return an error if single-session mode setting
		// is enabled on the instance.
		// Important note: the read-only check is performed outside the in-progress database transaction.
		if apiErr := s.enforceSingleSessionMode(ctx, env, currentClient); apiErr != nil {
			return nil, nil, apiErr
		}
	}

	// Identify step:
	// Uses the identifier (if any) to find the corresponding attribute.
	// If a strategy is used, use the strategy to find it, i.e. if the strategy is `email_code`
	// we expect the identifier to be an email address and nothing else.
	// Otherwise, we go over all the enabled attributes of the user settings and try to see
	// in which attribute the given identifier can fit in.
	strategy, formErrs := validateAndRetrieveStrategy(&signInForm, userSettings)
	signInAttribute, identifierErr := validateAndRetrieveSignInAttribute(signInForm.Identifier, strategy, userSettings)
	formErrs = apierror.Combine(formErrs, identifierErr)

	if formErrs != nil {
		return nil, nil, formErrs
	}

	// Regardless of what happens next, clear out the previous SignIn if it exists
	if currentClient != nil && currentClient.SignInID.Valid {
		currentClient.SignInID = null.StringFromPtr(nil)
		cdsClient := client_data.NewClientFromClientModel(currentClient)
		if err := s.clientDataService.UpdateClientSignInID(ctx, env.Instance.ID, cdsClient); err != nil {
			return nil, currentClient, apierror.Unexpected(err)
		}
		cdsClient.CopyToClientModel(currentClient)
	}

	// Create a new client, if there isn't one, otherwise reuse the current client.
	client, err := s.createClientIfMissing(ctx, env.Instance, currentClient)
	if err != nil {
		return nil, currentClient, apierror.Unexpected(err)
	}

	// Return known errors after the cookie drop, if a new client was created.
	newClientCreated := client != nil && client != currentClient

	// Create a new SignIn and add it to the Client
	clientDataClient := &client_data.Client{}
	clientDataClient.CopyFromClientModel(client)
	signIn, err := s.createSignIn(ctx, env.Instance, clientDataClient, deviceActivity)
	if err != nil {
		return nil, nil, apierror.Unexpected(err)
	}
	clientDataClient.CopyToClientModel(client)

	newSessionCreated := false
	var newSession *model.Session
	var attemptor sharedstrategies.Attemptor
	txErr := s.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		// If transfer is present, check transfer value and execute it
		if signInForm.Transfer != nil {
			accTransferID := client.ToSignInAccountTransferID.Ptr()
			if accTransferID == nil {
				return true, apierror.AccountTransferInvalid()
			}
			// remove transfer obj. (consumed)
			client.ToSignInAccountTransferID = null.StringFromPtr(nil)
			cdsClient := client_data.NewClientFromClientModel(client)
			// Note: the following operation happens outside the database transaction.
			// On rollback, the Client will not retain the association with the Account Transfer.
			if err := s.clientDataService.UpdateClientToSignInAccountTransferID(ctx, cdsClient); err != nil {
				return true, err
			}
			cdsClient.CopyToClientModel(client)

			newSession, newSessionCreated, err = s.performTransfer(ctx, tx, env, *accTransferID, client, signIn)
			return err != nil, err
		}

		// If we have found an attribute during the identify step, use the AddToSignIn
		// method of the attribute to populate the sign in.
		// The sign in is populated differently based on the behaviour of the respective attribute.
		// For example, the phone number will populate the IdentificationID, whereas the
		// email address can either populate the SAMLConnectionID or the IdentificationID,
		// depending on whether there exists an active SAML connection or not.
		if signInAttribute != nil {
			apiErr := signInAttribute.AddToSignIn(ctx, tx, env, usersettings.SignInForm{
				Identifier: *signInForm.Identifier,
			}, signIn)
			if apiErr != nil {
				return true, apiErr
			}
		}

		// If the IdentificationID of the sign in is populated, retrieve the actual identification.
		var identification *model.Identification
		if signIn.IdentificationID.Valid {
			identification, err = s.identificationRepo.FindByID(ctx, tx, signIn.IdentificationID.String)
			if err != nil {
				return true, err
			}

			// Check if the identifier belongs to a user that is already signed in the current Client.
			// Important Note: the read-only check is performed outside the in-progress database transaction
			// and breaks out of the transaction isolation.
			currentUserSession, err := s.clientActiveUserSession(ctx, env, client, identification.UserID.String)
			if err != nil {
				return true, apierror.Unexpected(err)
			}
			if currentUserSession != nil {
				return false, apierror.AlreadySignedIn(currentUserSession.ID)
			}
		}

		var user *model.User

		// Load user if identification is already known before the attempt
		if identification != nil {
			user, err = s.userRepo.FindByIDAndInstance(ctx, tx, identification.UserID.String, env.Instance.ID)
			if err != nil {
				return true, err
			}

			// Abort early if user is locked to prevent any verification preparation or attempt from taking place
			if user != nil {
				apiErr := s.userLockoutService.EnsureUserNotLocked(ctx, tx, env, user)
				if apiErr != nil {
					return false, apiErr
				}
			}
		}

		// Check if identifier is allowed based on the instance restrictions, i.e. allowlists/blocklists
		if identification != nil && identification.Identifier.Valid {
			if apiErr := s.enforceInstanceRestrictions(ctx, tx, env, identification); apiErr != nil {
				return true, apiErr
			}
		}

		var verification *model.Verification
		signInVerified := false
		var noRollbackError error
		if strategy != nil {
			if strategies.IsPreparableDuringSignIn(strategy) {
				// If we have a sign in preparable strategy, apply it.
				prepareForm := signInForm.ToPrepareForm(identification, client.ID)
				preparer, apiErr := strategy.(strategies.SignInPreparable).CreateSignInPreparer(ctx, tx, s.deps, env, signIn, prepareForm)
				if apiErr != nil {
					return true, apiErr
				}

				verification, err = preparer.Prepare(ctx, tx)
				if err != nil {
					return true, err
				}
			} else if strategies.IsAttemptableDuringSignIn(strategy) {
				// If we have a sign in attemptable strategy, apply it.
				attemptForm := signInForm.ToAttemptForm()
				var apiErr apierror.Error
				attemptor, apiErr = strategy.(strategies.SignInAttemptable).CreateSignInAttemptor(ctx, tx, s.deps, env, signIn.FirstFactorCurrentVerificationID, signIn, attemptForm)
				if apiErr != nil {
					return true, apiErr
				}

				verification, err = s.attemptVerificationWithLockout(ctx, tx, attemptor, client, env, user)
				if errors.Is(err, sharedstrategies.ErrInvalidPassword) {
					noRollbackError = err
				} else if errors.Is(err, sharedstrategies.ErrPwnedPassword) {
					// Propagate this error to the client if they can perform a reset, otherwise ignore it

					canReset, canResetErr := s.canResetPassword(ctx, tx, user, signIn, userSettings)
					if canResetErr != nil {
						return true, canResetErr
					}

					if canReset {
						noRollbackError = err
					} else {
						signInVerified = true
					}
				} else if err != nil {
					return true, err
				} else {
					signInVerified = true
				}

				// Some strategies won't require an identifier because they will instruct
				// which identification to be used. An example of this is `ticket`.
				// In such cases, we won't have an identification already loaded (because
				// there wasn't any identifier), so after the attempt, we'll need to load
				// the identification that was populated in the sign in by the attemptor.
				if identification == nil && signIn.IdentificationID.Valid {
					identification, err = s.identificationRepo.FindByID(ctx, tx, signIn.IdentificationID.String)
					if err != nil {
						return true, err
					}

					// Also load user now that identification has been determined
					user, err = s.userRepo.FindByIDAndInstance(ctx, tx, identification.UserID.String, env.Instance.ID)
					if err != nil {
						return true, err
					}

					// Enforce instance restrictions only in the case of a SAML IdP-initiated or Google One Tap flow
					if signIn.HasSAMLConnection() || strategy.Name() == constants.VSGoogleOneTap {
						emailIdentification, err := s.identificationRepo.FindByID(ctx, tx, identification.TargetIdentificationID.String)
						if err != nil {
							return true, err
						}

						if apiErr := s.enforceInstanceRestrictions(ctx, tx, env, emailIdentification); apiErr != nil {
							return true, apiErr
						}
					}
				}
			}

			// If there is a verification that was created (i.e. prepare or attempt ran),
			// attach it to the sign in.
			if verification != nil {
				if err := s.signInService.AttachFirstFactorVerification(ctx, tx, signIn, verification.ID, signInVerified); err != nil {
					return true, err
				}
			}

			// If we had an error during prepare or attempt that was non-rollbackable,
			// e.g. invalid password, now it's time to end the transaction (but not
			// rollback).
			// The reason we didn't do it earlier was in order to attach the newly created
			// verification in sign in.
			if noRollbackError != nil {
				return false, noRollbackError
			}
		}

		// Check if the sign in is complete and we are ready to convert it
		// to a session
		readyToConvert, err := s.signInService.IsReadyToConvert(ctx, tx, signIn, userSettings)
		if err != nil {
			return true, err
		}

		if readyToConvert && user != nil {
			// Sign in is complete. Let's convert it to a session.
			newSession, err = s.signInService.ConvertToSession(
				ctx,
				tx,
				sign_in.ConvertToSessionParams{
					Client:               client,
					Env:                  env,
					SignIn:               signIn,
					User:                 user,
					PostponeCookieUpdate: false,
				},
			)
			if err != nil {
				return true, err
			}
			newSessionCreated = true
		}

		return false, nil
	})
	if txErr != nil {
		// if there is a currentClient, reload it to revert any un-persisted changes
		if currentClient != nil {
			cdsClient, err := s.clientDataService.FindClient(ctx, env.Instance.ID, currentClient.ID)
			if err != nil {
				return nil, currentClient, apierror.Unexpected(err)
			}
			cdsClient.CopyToClientModel(currentClient)
		}
		if signIn != nil {
			err := s.signInRepo.Reload(ctx, s.deps.DB(), signIn)
			if err != nil {
				return nil, currentClient, apierror.Unexpected(err)
			}
		}

		var err apierror.Error
		var oauthErr clerkerrors.OAuthConfigMissing

		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			err = apiErr
		} else if errors.Is(txErr, sharedstrategies.ErrInvalidStrategyForVerification) {
			err = apierror.VerificationInvalidStrategy()
		} else if errors.As(txErr, &oauthErr) {
			err = apierror.OAuthConfigMissing(oauthErr.Provider)
		} else if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			err = apiErr
		} else if errors.Is(txErr, sharedstrategies.ErrFailedExchangeCredentialsOAuth1) {
			err = apierror.MisconfiguredOAuthProvider()
		} else if errors.Is(txErr, sharedstrategies.ErrSharedCredentialsNotAvailable) {
			err = apierror.OAuthSharedCredentialsNotSupported()
		} else if attemptor != nil {
			err = attemptor.ToAPIError(txErr)
		} else if errors.Is(txErr, saml.ErrConnectionNotFound) {
			err = apierror.SAMLNotEnabled(param.Identifier.Name)
		} else if errors.Is(txErr, saml.ErrInvalidIdentifier) {
			err = apierror.FormInvalidEmailAddress(param.Identifier.Name)
		} else {
			err = apierror.Unexpected(txErr)
		}

		// if it's an expected error and there is a new client created, return the client along with the error
		if newClientCreated {
			return signIn, client, err
		}
		return nil, nil, err
	}

	if env.Instance.IsDevelopment() && newSessionCreated {
		fapi.EnqueueSegmentEvent(ctx, s.deps.GueClient(), fapi.SegmentParams{EventName: segment.APIFrontendSessionCreated})
	}

	if newSessionCreated && newSession != nil {
		if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
			return nil, nil, apierror.Unexpected(err)
		}
	}

	if newClientCreated || newSessionCreated {
		return signIn, client, nil
	}

	return signIn, nil, nil
}

func validateAndRetrieveStrategy(createForm *SignInCreateForm, userSettings *usersettings.UserSettings) (strategies.SignInStrategy, apierror.Error) {
	if createForm.Strategy == nil {
		if createForm.Password != nil {
			// Password has special treatment in a way that if it's not passed as a strategy
			// but an actual password is given, then we force it as a strategy.
			forcedStrategy := constants.VSPassword
			createForm.Strategy = &forcedStrategy
		} else {
			return nil, nil
		}
	}

	if !userSettings.FirstFactors().Contains(*createForm.Strategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, *createForm.Strategy)
	}

	strategy, strategyExists := strategies.GetStrategy(*createForm.Strategy)
	if !strategyExists {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, *createForm.Strategy)
	}

	if !strategies.IsSignInStrategy(strategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, *createForm.Strategy)
	}

	return strategy.(strategies.SignInStrategy), nil
}

func validateAndRetrieveSignInAttribute(
	identifier *string,
	strategy strategies.SignInStrategy,
	userSettings *usersettings.UserSettings) (usersettings.SignInAttribute, apierror.Error) {
	if strategy != nil {
		// We have a strategy, use it to try and identify the attribute
		// that corresponds to the given identifier
		return strategy.IdentifyAttribute(identifier, userSettings)
	}
	if identifier == nil {
		return nil, nil
	}

	// There is an empty identifier in the payload, try to build an appropriate
	// error message to return.
	// Note: This monstrosity is there because older versions would send an empty
	// string when the user would click Continue without inputting any values.
	// In these cases, we wanted to return an error message that would contain all
	// the allowed identification types.
	// Is this ugly? Yes
	// Confusing? Definitely
	// Localizable: Most certainly not
	// Alright, shall we get rid of it then? No, because we will be breaking older apps
	if *identifier == "" {
		identificationStrategies := make([]string, 0)
		for _, attribute := range userSettings.EnabledAttributes() {
			if !attribute.Base().UsedForFirstFactor {
				continue
			}
			identificationStrategies = append(identificationStrategies, cstrings.SnakeCaseToHumanReadableString(attribute.Name()))
		}

		errMsg := cstrings.ToSentence(identificationStrategies, ", ", " or ")
		return nil, apierror.FormNilParameterWithCustomText(param.Identifier.Name, errMsg)
	}

	// All we have is an identifier. Use the enabled attributes of the user settings
	// to try and find which one corresponds to the given identifier.
	attribute, apiErr := usersettings.DetectUserSettingsAttribute(*identifier, userSettings)
	if apiErr != nil {
		return nil, apiErr
	}
	signInAttribute, ok := attribute.(usersettings.SignInAttribute)
	if !ok {
		return nil, apierror.Unexpected(fmt.Errorf("signIn/create: %s is not a sign in attribute", attribute.Name()))
	}
	return signInAttribute, nil
}

func (s *Service) createClientIfMissing(ctx context.Context, instance *model.Instance, client *model.Client) (*model.Client, error) {
	// If there is no client, create one
	if client == nil {
		var devBrowserID *string
		if dvb := requestingdevbrowser.FromContext(ctx); dvb != nil {
			devBrowserID = &dvb.ID
		}
		newClient, err := s.clientService.CreateWithDevBrowser(ctx, instance, devBrowserID)
		if err != nil {
			return nil, err
		}
		client = newClient
	}

	return client, nil
}

func (s *Service) createSignIn(
	ctx context.Context,
	instance *model.Instance,
	client *client_data.Client,
	deviceActivity *model.SessionActivity) (*model.SignIn, error) {
	err := s.sessionActivitiesService.CreateSessionActivity(ctx, s.deps.DB(), instance.ID, deviceActivity)
	if err != nil {
		return nil, err
	}

	// Create a new sign in
	newSignIn := &model.SignIn{SignIn: &sqbmodel.SignIn{
		InstanceID:        instance.ID,
		ClientID:          client.ID,
		AuthConfigID:      instance.ActiveAuthConfigID,
		SessionActivityID: null.StringFrom(deviceActivity.ID),
		AbandonAt:         s.deps.Clock().Now().UTC().Add(time.Second * time.Duration(constants.ExpiryTimeMediumShort)),
	}}
	if err := s.signInRepo.Insert(ctx, s.deps.DB(), newSignIn); err != nil {
		return nil, err
	}

	// Update sign in id in client
	client.SignInID = null.StringFrom(newSignIn.ID)
	if err = s.clientDataService.UpdateClientSignInID(ctx, instance.ID, client); err != nil {
		return nil, err
	}

	return newSignIn, nil
}

func (s *Service) performTransfer(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	accountTransferID string,
	client *model.Client,
	signIn *model.SignIn,
) (*model.Session, bool, error) {
	accTransfer, err := s.accountTransferRepo.QueryByInstanceAndID(ctx, tx, env.Instance.ID, accountTransferID)
	if err != nil {
		return nil, false, err
	}
	if accTransfer == nil {
		return nil, false, apierror.AccountTransferInvalid()
	}

	ident, err := s.identificationRepo.QueryByID(ctx, tx, accTransfer.IdentificationID)
	if err != nil {
		return nil, false, apierror.Unexpected(err)
	} else if ident == nil {
		return nil, false, apierror.SignInIdentificationOrUserDeleted()
	}

	if err = s.updateSignInWithIdentificationAndVerification(ctx, tx, signIn, accTransfer, ident); err != nil {
		if errors.Is(err, verifications.ErrVerificationNotFound) {
			return nil, false, apierror.AccountTransferInvalid()
		}
		return nil, false, err
	}

	// get user to convert to session
	user, err := s.userRepo.FindByInstanceAndIdentificationID(ctx, tx, env.Instance.ID, accTransfer.IdentificationID)
	if err != nil {
		return nil, false, err
	}

	if err = s.userService.SyncSignInPasswordReset(ctx, tx, env.Instance, signIn, user); err != nil {
		return nil, false, err
	}

	readyToConvert, err := s.signInService.IsReadyToConvert(ctx, tx, signIn, usersettings.NewUserSettings(env.AuthConfig.UserSettings))
	if err != nil {
		return nil, false, err
	}

	if !readyToConvert {
		return nil, false, nil
	}

	newSession, err := s.signInService.ConvertToSession(
		ctx,
		tx,
		sign_in.ConvertToSessionParams{
			Client:               client,
			Env:                  env,
			SignIn:               signIn,
			User:                 user,
			PostponeCookieUpdate: false,
			FromTransfer:         true,
		})
	if err != nil {
		return nil, false, err
	}

	return newSession, true, nil
}

// updateSignInWithIdentificationAndVerification updates the signIn object and handles
// updating verification when needed.
func (s *Service) updateSignInWithIdentificationAndVerification(ctx context.Context, tx database.Tx, signIn *model.SignIn, accTransfer *model.AccountTransfer, ident *model.Identification) error {
	signIn.IdentificationID = null.StringFrom(accTransfer.IdentificationID)
	whitelistCols := set.New[string](sqbmodel.SignInColumns.IdentificationID)

	// Transfer relevant data from the account transfer to the sign-in model when transferring an unverified email.
	// Also, clear verification only when identification doesn't require it.
	if accTransfer.ToLinkIdentificationID.Valid {
		signIn.ToLinkIdentificationID = accTransfer.ToLinkIdentificationID
		whitelistCols.Insert(sqbmodel.SignInColumns.ToLinkIdentificationID)
	} else if !ident.RequiresVerification.Bool {
		ver, err := s.verificationService.Clear(ctx, tx, accTransfer.ID)
		if err != nil {
			return err
		}

		signIn.FirstFactorSuccessVerificationID = null.StringFrom(ver.ID)
		whitelistCols.Insert(sqbmodel.SignInColumns.FirstFactorSuccessVerificationID)
	}

	return s.signInRepo.Update(ctx, tx, signIn, whitelistCols.Array()...)
}

type ResetPasswordParams struct {
	Password               string
	SignOutOfOtherSessions bool
}

// ResetPassword accepts the new password for a user as part of their sign-in process
// and updates the sign in object with the new password.
// Keep in mind that for the new password digest, we always use `bcrypt`. Also, the
// password of the user is only updated when the sign in is completed.
func (s *Service) ResetPassword(ctx context.Context, params ResetPasswordParams) (*model.SignIn, *model.Client, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := env.AuthConfig.UserSettings
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	signIn := ctx.Value(ctxkeys.SignIn).(*model.SignIn)

	// Return error if:
	// * User hasn't requested a password reset
	// * User hasn't passed first factor verification (e.g. OTP)
	if signIn.Status(s.deps.Clock()) != constants.SignInNeedsNewPassword {
		return nil, nil, apierror.InvalidClientStateForAction("Reset Password", "Please initiate the reset password flow by calling `prepare_first_factor` with `reset_password_code` strategy.")
	}

	apiErr := validate.Password(ctx, params.Password, param.Password.Name, userSettings.PasswordSettings)
	if apiErr != nil {
		return nil, nil, apiErr
	}

	passwordDigest, err := hash.GenerateBcryptHash(params.Password)
	if err != nil {
		return nil, nil, apierror.Unexpected(err)
	}

	newSessionCreated := false
	var newSession *model.Session
	txErr := s.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		signIn.RequiresNewPassword = false
		signIn.NewPasswordDigest = null.StringFrom(passwordDigest)
		signIn.SignOutOfOtherSessions = params.SignOutOfOtherSessions

		err := s.signInRepo.UpdateResetPasswordColumns(ctx, tx, signIn)
		if err != nil {
			return true, err
		}

		// Check if sign in can be converted and convert it to session
		readyToConvert, err := s.signInService.IsReadyToConvert(ctx, tx, signIn, usersettings.NewUserSettings(userSettings))
		if err != nil {
			return true, err
		}

		if readyToConvert {
			user, err := s.userRepo.FindByIdentification(ctx, tx, signIn.IdentificationID.String)
			if err != nil {
				return true, err
			}

			user.RequiresNewPassword = null.BoolFromPtr(nil)
			if err = s.userRepo.UpdateRequiresNewPassword(ctx, tx, user); err != nil {
				return true, err
			}

			newSession, err = s.signInService.ConvertToSession(
				ctx,
				tx,
				sign_in.ConvertToSessionParams{
					Client:               client,
					Env:                  env,
					SignIn:               signIn,
					User:                 user,
					PostponeCookieUpdate: false,
				})
			if err != nil {
				return true, err
			}

			newSessionCreated = true
		}

		return err != nil, err
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, nil, apiErr
		}
		return nil, nil, apierror.Unexpected(txErr)
	}

	if newSessionCreated {
		if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
			return nil, nil, apierror.Unexpected(err)
		}
		return signIn, client, nil
	}

	return signIn, nil, nil
}

// PrepareFirstFactor prepares the first factor for the current sign in
func (s *Service) PrepareFirstFactor(ctx context.Context, prepareForm strategies.SignInPrepareForm) (*model.SignIn, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	signIn := ctx.Value(ctxkeys.SignIn).(*model.SignIn)

	if prepareForm.Strategy == constants.VSPasskey && !cenv.ResourceHasAccess(cenv.FlagAllowPasskeysInstanceIDs, env.Instance.ID) {
		return nil, apierror.FeatureNotEnabled()
	}

	// Find the given strategy and make sure it can be used as first
	// factor in prepare phase of sign in
	if !userSettings.FirstFactors().Contains(prepareForm.Strategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, prepareForm.Strategy)
	}

	selectedStrategy, strategyExists := strategies.GetStrategy(prepareForm.Strategy)
	if !strategyExists || !strategies.IsPreparableDuringSignIn(selectedStrategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, prepareForm.Strategy)
	}
	strategy := selectedStrategy.(strategies.SignInPreparable)

	txErr := s.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// Create the preparer for the given strategy
		preparer, apiErr := strategy.CreateSignInPreparer(ctx, tx, s.deps, env, signIn, prepareForm)
		if apiErr != nil {
			return true, apiErr
		}

		if preparer.Identification() != nil {
			// Make sure we have an identification in sign in
			if !signIn.IdentificationID.Valid {
				return true, apierror.InvalidClientStateForAction(
					"Factor One Preparation",
					"This Sign In Attempt is not Identified, please identify first.",
				)
			}

			// Check that the sign in identification and the prepare
			// identification belong to the same user
			signInIdentification, err := s.identificationRepo.FindByID(ctx, tx, signIn.IdentificationID.String)
			if err != nil {
				return true, err
			}

			if signInIdentification.UserID.String != preparer.Identification().UserID.String {
				return true, apierror.IdentificationBelongsToDifferentUser()
			}
		}

		// Perform the actual prepare process
		verification, err := preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		// Attach the new verification to the sign in
		if err := s.signInService.AttachFirstFactorVerification(ctx, tx, signIn, verification.ID, false); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return signIn, nil
}

// AttemptFirstFactor attempts to verify the prepared first factor for the current sign-in
func (s *Service) AttemptFirstFactor(ctx context.Context, attemptForm strategies.SignInAttemptForm) (*model.SignIn, *model.Client, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	signIn := ctx.Value(ctxkeys.SignIn).(*model.SignIn)

	if attemptForm.Strategy == constants.VSPasskey && !cenv.ResourceHasAccess(cenv.FlagAllowPasskeysInstanceIDs, env.Instance.ID) {
		return nil, nil, apierror.FeatureNotEnabled()
	}

	// Check whether the given strategy is one of the allowed first factors
	if !userSettings.FirstFactors().Contains(attemptForm.Strategy) {
		return nil, nil, apierror.FormInvalidParameterValue(param.Strategy.Name, attemptForm.Strategy)
	}

	selectedStrategy, strategyExists := strategies.GetStrategy(attemptForm.Strategy)
	if !strategyExists || !strategies.IsAttemptableDuringSignIn(selectedStrategy) {
		return nil, nil, apierror.FormInvalidParameterValue(param.Strategy.Name, attemptForm.Strategy)
	}
	strategy := selectedStrategy.(strategies.SignInAttemptable)

	// Check that the sign in already has an identification
	if strategy.NeedsIdentificationForAttemptor() && !signIn.IdentificationID.Valid {
		return nil, nil, apierror.InvalidClientStateForAction("Factor One Verification", "This Sign In Attempt is not Identified, please identify first.")
	}

	newSessionCreated := false
	var newSession *model.Session
	var attemptor sharedstrategies.Attemptor
	txErr := s.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var user *model.User
		var err error
		if strategy.NeedsIdentificationForAttemptor() {
			user, err = s.userRepo.FindByIdentification(ctx, tx, signIn.IdentificationID.String)
			if err != nil {
				return true, err
			}
		}

		// Create attemptor for the given strategy
		var apiErr apierror.Error
		attemptor, apiErr = strategy.CreateSignInAttemptor(ctx, tx, s.deps, env, signIn.FirstFactorCurrentVerificationID, signIn, attemptForm)
		if apiErr != nil {
			return true, apiErr
		}

		// Attempt to verify
		verification, err := s.attemptVerificationWithLockout(
			ctx,
			tx,
			attemptor,
			client,
			env,
			user,
		)

		if errors.Is(err, sharedstrategies.ErrInvalidCode) {
			return false, err
		} else if errors.Is(err, sharedstrategies.ErrInvalidPassword) {
			return false, err
		} else if errors.Is(err, sharedstrategies.ErrPwnedPassword) {
			// Propagate this error to the client if they can perform a reset, otherwise ignore it

			canReset, canResetErr := s.canResetPassword(ctx, tx, user, signIn, userSettings)
			if canResetErr != nil {
				return true, canResetErr
			}

			if canReset {
				return false, err
			}
		} else if err != nil {
			return true, err
		}

		// For strategies that don't need the identification before attempt,
		// it should now be populated (e.g. passkeys)
		if !strategy.NeedsIdentificationForAttemptor() {
			user, err = s.userRepo.FindByIdentification(ctx, tx, signIn.IdentificationID.String)
			if err != nil {
				return true, err
			}
		}

		// Attach the verification to the sign in
		if err := s.signInService.AttachFirstFactorVerification(ctx, tx, signIn, verification.ID, true); err != nil {
			return true, err
		}

		// Check if sign in can be converted and convert it to session
		readyToConvert, err := s.signInService.IsReadyToConvert(ctx, tx, signIn, userSettings)
		if err != nil {
			return true, err
		}

		if readyToConvert {
			fromTransfer, err := s.isLinkIdentificationFromTransfer(ctx, tx, signIn)
			if err != nil {
				return true, err
			}

			if err = s.signInService.ResetReVerificationState(ctx, tx, signIn); err != nil {
				return true, err
			}

			if err = s.signInService.LinkIdentificationToUser(ctx, tx, signIn, user.ID); err != nil {
				return true, err
			}

			newSession, err = s.signInService.ConvertToSession(
				ctx,
				tx,
				sign_in.ConvertToSessionParams{
					Client:               client,
					Env:                  env,
					SignIn:               signIn,
					User:                 user,
					PostponeCookieUpdate: false,
					FromTransfer:         fromTransfer,
				})
			if err != nil {
				return true, err
			}

			newSessionCreated = true
		}

		return false, nil
	})
	if txErr != nil {
		if apierror, ok := apierror.As(txErr); ok {
			return nil, nil, apierror
		} else if attemptor != nil {
			return nil, nil, attemptor.ToAPIError(txErr)
		}
		return nil, nil, apierror.Unexpected(txErr)
	}

	if newSessionCreated && newSession != nil {
		if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
			return nil, nil, apierror.Unexpected(err)
		}
		return signIn, client, nil
	}

	return signIn, nil, nil
}

func (s *Service) isLinkIdentificationFromTransfer(ctx context.Context, tx database.Tx, signIn *model.SignIn) (bool, error) {
	accTransfer, err := s.accountTransferRepo.QueryByInstanceAndToLinkIdentificationID(ctx, tx, signIn.InstanceID, signIn.ToLinkIdentificationID.String)
	return accTransfer != nil, err
}

// PrepareSecondFactor prepares the second factor for the current sign-in
func (s *Service) PrepareSecondFactor(ctx context.Context, prepareForm strategies.SignInPrepareForm) (*model.SignIn, apierror.Error) {
	env := environment.FromContext(ctx)
	signIn := ctx.Value(ctxkeys.SignIn).(*model.SignIn)

	// Validate sign in is in the correct state for this action
	status := signIn.Status(s.deps.Clock())
	if status != constants.SignInNeedsSecondFactor {
		return nil, apierror.InvalidClientStateForAction(
			"Factor Two Verification",
			"This Sign In Attempt is not ready for factor two verification.",
		)
	}

	// Make sure we have an identification in sign in
	if !signIn.IdentificationID.Valid {
		return nil, apierror.InvalidClientStateForAction(
			"Factor Two Preparation",
			"This Sign In Attempt is not Identified, please identify first.",
		)
	}

	// TODO: The snippet below needs to be removed.
	// It's only there to support calls from older ClerkJS versions that
	// don't supply the `phone_number_id`.
	if prepareForm.PhoneNumberID == nil {
		phoneNumber, apierr := s.legacySelectSecondFactorIdentification(ctx, signIn, prepareForm.Strategy)
		if apierr != nil {
			return nil, apierr
		}
		prepareForm.PhoneNumberID = &phoneNumber.ID
	}

	// Find the given strategy and make sure it can be used as second
	// factor in prepare phase of sign in
	selectedStrategy, strategyExists := strategies.GetStrategy(prepareForm.Strategy)
	if !strategyExists || !strategies.IsPreparableDuringSignIn(selectedStrategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, prepareForm.Strategy)
	}
	strategy := selectedStrategy.(strategies.SignInPreparable)

	txErr := s.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// Create the preparer for the selected strategy
		preparer, apiErr := strategy.CreateSignInPreparer(ctx, tx, s.deps, env, signIn, prepareForm)
		if apiErr != nil {
			return true, apiErr
		}

		// Check that the sign in identification and the prepare
		// identification belong to the same user
		signInIdentification, err := s.identificationRepo.FindByID(ctx, tx, signIn.IdentificationID.String)
		if err != nil {
			return true, err
		}

		if signInIdentification.UserID.String != preparer.Identification().UserID.String {
			return true, apierror.IdentificationBelongsToDifferentUser()
		}

		// Perform the actual prepare process
		verification, err := preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		if err := s.attachSecondFactorVerification(ctx, tx, signIn, verification.ID, false); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if apierror, ok := apierror.As(txErr); ok {
			return nil, apierror
		}
		return nil, apierror.Unexpected(txErr)
	}

	return signIn, nil
}

func (s *Service) legacySelectSecondFactorIdentification(
	ctx context.Context,
	signIn *model.SignIn,
	strategy string) (*model.Identification, apierror.Error) {
	signInIdentification, err := s.identificationRepo.FindByID(ctx, s.deps.DB(), signIn.IdentificationID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	user, err := s.userRepo.QueryByID(ctx, s.deps.DB(), signInIdentification.UserID.String)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UserNotFound(signInIdentification.UserID.String)
	}

	allSecondFactors, err := s.identificationRepo.FindAllSecondFactorsByUser(ctx, s.deps.DB(), user.ID)
	if err != nil {
		return nil, apierror.Unexpected(fmt.Errorf("factorSignIn/legacySelectSecondFactorIdentification: retrieving all second factors for user %s: %w",
			user.ID, err))
	}

	var correctIdentification *model.Identification
	for _, secondFactor := range allSecondFactors {
		switch strategy {
		case constants.VSPhoneCode:
			if secondFactor.Type != constants.ITPhoneNumber {
				continue
			}
			// choose the first applicable second factor or the default one (if there is one)
			if secondFactor.DefaultSecondFactor {
				return secondFactor, nil
			} else if correctIdentification == nil {
				correctIdentification = secondFactor
			}
		}
	}

	if correctIdentification == nil {
		return nil, apierror.NoSecondFactorsForStrategy(strategy)
	}

	return correctIdentification, nil
}

func (s *Service) attachSecondFactorVerification(ctx context.Context, exec database.Executor,
	signIn *model.SignIn, verificationID string, verified bool) error {
	columns := []string{
		sqbmodel.SignInColumns.SecondFactorCurrentVerificationID,
		sqbmodel.SignInColumns.SecondFactorSuccessVerificationID,
	}

	if verified {
		signIn.SecondFactorCurrentVerificationID = null.StringFromPtr(nil)
		signIn.SecondFactorSuccessVerificationID = null.StringFrom(verificationID)
	} else {
		signIn.SecondFactorCurrentVerificationID = null.StringFrom(verificationID)
		signIn.SecondFactorSuccessVerificationID = null.StringFromPtr(nil)
	}

	return s.signInRepo.Update(ctx, exec, signIn, columns...)
}

// AttemptSecondFactor attempts to verify the prepare second factor for the current sign-in
func (s *Service) AttemptSecondFactor(ctx context.Context, attemptForm strategies.SignInAttemptForm) (*model.SignIn, *model.Client, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	signIn := ctx.Value(ctxkeys.SignIn).(*model.SignIn)

	// Check whether the given strategy is one of the allowed first factors
	if !userSettings.SecondFactors().Contains(attemptForm.Strategy) {
		return nil, nil, apierror.FormInvalidParameterValue(param.Strategy.Name, attemptForm.Strategy)
	}

	selectedStrategy, strategyExists := strategies.GetStrategy(attemptForm.Strategy)
	if !strategyExists || !strategies.IsAttemptableDuringSignIn(selectedStrategy) {
		return nil, nil, apierror.FormInvalidParameterValue(param.Strategy.Name, attemptForm.Strategy)
	}
	strategy := selectedStrategy.(strategies.SignInAttemptable)

	// Check that the sign in already has an identification
	if !signIn.IdentificationID.Valid {
		return nil, nil, apierror.InvalidClientStateForAction("Factor Two Verification", "This Sign In Attempt is not Identified, please identify first.")
	}

	newSessionCreated := false
	var newSession *model.Session
	var attemptor sharedstrategies.Attemptor
	txErr := s.deps.DB().PerformTx(ctx, func(tx database.Tx) (bool, error) {
		user, err := s.userRepo.FindByIdentification(ctx, tx, signIn.IdentificationID.String)
		if err != nil {
			return true, err
		}

		// Create attemptor for the given strategy
		var apiErr apierror.Error
		attemptor, apiErr = strategy.CreateSignInAttemptor(ctx, tx, s.deps, env, signIn.SecondFactorCurrentVerificationID, signIn, attemptForm)
		if apiErr != nil {
			return true, apiErr
		}

		// Attempt to verify
		verification, err := s.attemptVerificationWithLockout(
			ctx,
			tx,
			attemptor,
			client,
			env,
			user,
		)
		if errors.Is(err, sharedstrategies.ErrInvalidCode) {
			return false, err
		} else if errors.Is(err, backup_codes.ErrInvalidCode) {
			return false, err
		} else if errors.Is(err, sharedstrategies.ErrExpired) {
			return false, err
		} else if errors.Is(err, sharedstrategies.ErrFailed) {
			return false, err
		} else if err != nil {
			return true, err
		}

		// at this point, the verification is verified
		if err := s.attachSecondFactorVerification(ctx, tx, signIn, verification.ID, true); err != nil {
			return true, err
		}

		newSession, err = s.signInService.ConvertToSession(
			ctx,
			tx,
			sign_in.ConvertToSessionParams{
				Client:               client,
				Env:                  env,
				SignIn:               signIn,
				User:                 user,
				PostponeCookieUpdate: false,
			},
		)
		if err != nil {
			return true, err
		}

		newSessionCreated = true
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, nil, apiErr
		} else if attemptor != nil {
			return nil, nil, attemptor.ToAPIError(txErr)
		}
		return nil, nil, apierror.Unexpected(txErr)
	}

	if newSessionCreated && newSession != nil {
		if err := s.sessionService.Activate(ctx, env.Instance, newSession); err != nil {
			return nil, nil, apierror.Unexpected(err)
		}
		return signIn, client, nil
	}

	return signIn, nil, nil
}

// EnsureUserNotLockedFromSignIn checks if user is locked if provided with a sign-in.
func (s *Service) EnsureUserNotLockedFromSignIn(ctx context.Context, exec database.Executor, signIn *model.SignIn) apierror.Error {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.UserLockoutEnabled() {
		return nil
	}

	if !signIn.IdentificationID.Valid {
		return nil
	}

	identification, err := s.identificationRepo.FindByIDAndInstance(ctx, exec, signIn.IdentificationID.String, env.Instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	return s.ensureUserNotLockedFromIdentification(ctx, exec, env, identification)
}

// ensureUserNotLockedFromIdentification checks if user is locked if provided with an identification.
func (s *Service) ensureUserNotLockedFromIdentification(ctx context.Context, exec database.Executor, env *model.Env, identification *model.Identification) apierror.Error {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.UserLockoutEnabled() {
		return nil
	}

	if !identification.UserID.Valid {
		return nil
	}

	user, err := s.userRepo.QueryByIDAndInstance(ctx, exec, identification.UserID.String, env.Instance.ID)
	if err != nil {
		return apierror.Unexpected(err)
	} else if user == nil {
		return apierror.UserNotFound(identification.UserID.String)
	}

	return s.userLockoutService.EnsureUserNotLocked(ctx, exec, env, user)
}

func (s *Service) attemptVerificationWithLockout(ctx context.Context,
	tx database.Tx,
	attemptor sharedstrategies.Attemptor,
	client *model.Client,
	env *model.Env,
	user *model.User) (*model.Verification, error) {
	verification, err := sharedstrategies.AttemptVerification(ctx, tx, attemptor, s.verificationRepo, client.ID)
	if err != nil {
		if errors.Is(err, sharedstrategies.ErrInvalidPassword) || errors.Is(err, sharedstrategies.ErrInvalidCode) || errors.Is(err, backup_codes.ErrInvalidCode) {
			incErr := s.userLockoutService.IncrementFailedVerificationAttempts(ctx, tx, env, user)
			if incErr != nil {
				return verification, incErr
			}
		}

		return verification, err
	}

	return verification, nil
}

func (s *Service) enforceSingleSessionMode(ctx context.Context, env *model.Env, client *model.Client) apierror.Error {
	if !env.AuthConfig.SessionSettings.SingleSessionMode {
		return nil
	}
	clientActiveSessions, err := s.clientDataService.FindAllClientSessions(ctx, env.Instance.ID, client.ID, client_data.SessionFilterActiveOnly())
	if err != nil {
		return apierror.Unexpected(err)
	}
	if len(clientActiveSessions) > 0 {
		return apierror.SingleModeSessionExists()
	}
	return nil
}

func (s *Service) enforceInstanceRestrictions(ctx context.Context, tx database.Tx, env *model.Env, identification *model.Identification) apierror.Error {
	identification.SetCanonicalIdentifier()
	ident := restrictions.Identification{
		Identifier: identification.Identifier.String,
		Type:       identification.Type,
	}

	if identification.CanonicalIdentifier.Valid {
		ident.CanonicalIdentifier = identification.CanonicalIdentifier.String
	}

	res, err := s.restrictionService.Check(ctx, tx,
		ident,
		restrictions.Settings{
			Restrictions: usersettingsmodel.Restrictions{
				Allowlist: env.AuthConfig.UserSettings.Restrictions.Allowlist,
				Blocklist: env.AuthConfig.UserSettings.Restrictions.Blocklist,
			},
			TestMode: env.AuthConfig.TestMode,
		},
		env.Instance.ID,
	)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if res.Blocked || !res.Allowed {
		return apierror.IdentifierNotAllowedAccess(res.Identifier)
	}

	return nil
}

func (s *Service) clientActiveUserSession(ctx context.Context, env *model.Env, client *model.Client, userID string) (*model.Session, error) {
	clientActiveSessions, err := s.clientDataService.FindAllClientSessions(ctx, env.Instance.ID, client.ID, client_data.SessionFilterActiveOnly())
	if err != nil {
		return nil, err
	}
	if len(clientActiveSessions) == 0 {
		return nil, nil
	}
	var userSession *model.Session
	for _, sess := range clientActiveSessions {
		if sess.UserID == userID {
			userSession = sess.ToSessionModel()
			break
		}
	}
	return userSession, nil
}

// TODO(password_reset, mark, 2024-03-10) if the user cannot reset their password:
// * we should try to use other methods such as TOTP or backup codes
// * we should mark the user as having a pwned password so that the UI can display a warning

func (s *Service) canResetPassword(ctx context.Context, tx database.Tx, user *model.User, signIn *model.SignIn, userSettings *usersettings.UserSettings) (bool, error) {
	firstFactorIdentifications, err := s.identificationRepo.FindAllFirstFactorsByUser(ctx, tx, user.ID)
	if err != nil {
		return false, fmt.Errorf("signIn/canResetPassword: fetching first factor identifications for user %s: %w",
			user.ID, err)
	}

	firstFactors := userSettings.FirstFactors()

	userFirstFactors, err := s.signInService.Factors(ctx, tx, signIn, user, firstFactorIdentifications, firstFactors)
	if err != nil {
		return false, fmt.Errorf(
			"signIn/canResetPassword: fetching first factors for (%+v, %+v, %+v): %w", signIn, user, firstFactors, err)
	}

	for _, ff := range userFirstFactors {
		if resetPasswordStrategies.Contains(ff.Strategy) {
			return true, nil
		}
	}

	return false, nil
}
