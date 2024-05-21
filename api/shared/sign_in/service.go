package sign_in

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/shared/client_data"
	"clerk/api/shared/cookies"
	"clerk/api/shared/externalaccount"
	"clerk/api/shared/identifications"
	"clerk/api/shared/organizations"
	"clerk/api/shared/password"
	"clerk/api/shared/serializable"
	"clerk/api/shared/sessions"
	userlockout "clerk/api/shared/user_lockout"
	"clerk/api/shared/user_profile"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cenv"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/clerkjs_version"
	"clerk/pkg/hash"
	"clerk/pkg/jobs"
	"clerk/pkg/set"
	"clerk/pkg/strings"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/strategies"
	"clerk/pkg/versions"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock     clockwork.Clock
	gueClient *gue.Client
	db        database.Database

	// services
	cookieService          *cookies.Service
	externalAccountService *externalaccount.Service
	identificationService  *identifications.Service
	organizationService    *organizations.Service
	passwordService        *password.Service
	serializableService    *serializable.Service
	sessionService         *sessions.Service
	userLockoutService     *userlockout.Service
	userProfileService     *user_profile.Service
	verificationService    *verifications.Service
	clientDataService      *client_data.Service

	// repositories
	actorTokenRepo              *repository.ActorToken
	backupCodeRepo              *repository.BackupCode
	dailySuccessfulSignInRepo   *repository.DailySuccessfulSignIns
	externalAccountRepo         *repository.ExternalAccount
	identificationRepo          *repository.Identification
	invitationRepo              *repository.Invitations
	organizationInvitationsRepo *repository.OrganizationInvitation
	organizationMembershipsRepo *repository.OrganizationMembership
	signInRepo                  *repository.SignIn
	totpRepo                    *repository.TOTP
	userRepo                    *repository.Users
	verificationRepo            *repository.Verification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                       deps.Clock(),
		gueClient:                   deps.GueClient(),
		db:                          deps.DB(),
		cookieService:               cookies.NewService(deps),
		externalAccountService:      externalaccount.NewService(deps),
		identificationService:       identifications.NewService(deps),
		organizationService:         organizations.NewService(deps),
		passwordService:             password.NewService(deps),
		serializableService:         serializable.NewService(deps.Clock()),
		sessionService:              sessions.NewService(deps),
		userLockoutService:          userlockout.NewService(deps),
		userProfileService:          user_profile.NewService(deps.Clock()),
		verificationService:         verifications.NewService(deps.Clock()),
		clientDataService:           client_data.NewService(deps),
		actorTokenRepo:              repository.NewActorToken(),
		backupCodeRepo:              repository.NewBackupCode(),
		dailySuccessfulSignInRepo:   repository.NewDailySuccessfulSignIns(),
		externalAccountRepo:         repository.NewExternalAccount(),
		identificationRepo:          repository.NewIdentification(),
		invitationRepo:              repository.NewInvitations(),
		organizationInvitationsRepo: repository.NewOrganizationInvitation(),
		organizationMembershipsRepo: repository.NewOrganizationMembership(),
		signInRepo:                  repository.NewSignIn(),
		totpRepo:                    repository.NewTOTP(),
		userRepo:                    repository.NewUsers(),
		verificationRepo:            repository.NewVerification(),
	}
}

// IsReadyToConvert returns true for a signIn that can be completed, false
// otherwise.
// If there's a second factor strategy enabled in user settings and the user
// has enabled 2FA for their identification, the signIn needs a successful
// second factor verification before it can be completed.
// Otherwise, the signIn needs just a successful first factor verification.
func (s *Service) IsReadyToConvert(ctx context.Context, tx database.Tx, signIn *model.SignIn, userSettings *usersettings.UserSettings) (bool, error) {
	if !signIn.IdentificationID.Valid {
		return false, nil
	}
	if !signIn.FirstFactorSuccessVerificationID.Valid {
		return false, nil
	}
	if signIn.RequiresNewPassword {
		return false, nil
	}

	ident, err := s.identificationRepo.FindByID(ctx, tx, signIn.IdentificationID.String)
	if err != nil {
		return false, err
	}

	userHasTwoFactorEnabled, err := s.userProfileService.HasTwoFactorEnabled(ctx, tx, userSettings, ident.UserID.String)
	if err != nil {
		return false, err
	}
	if userHasTwoFactorEnabled {
		return signIn.SecondFactorSuccessVerificationID.Valid, nil
	}
	return true, nil
}

func (s *Service) AttachFirstFactorVerification(ctx context.Context, tx database.Tx, signIn *model.SignIn,
	verificationID string, verified bool) error {
	if verified {
		signIn.FirstFactorCurrentVerificationID = null.StringFromPtr(nil)
		signIn.FirstFactorSuccessVerificationID = null.StringFrom(verificationID)
	} else {
		signIn.FirstFactorCurrentVerificationID = null.StringFrom(verificationID)
		signIn.FirstFactorSuccessVerificationID = null.StringFromPtr(nil)
	}

	whitelistColumns := []string{
		sqbmodel.SignInColumns.FirstFactorCurrentVerificationID,
		sqbmodel.SignInColumns.FirstFactorSuccessVerificationID,
	}

	if err := s.signInRepo.Update(ctx, tx, signIn, whitelistColumns...); err != nil {
		return err
	}

	return s.clientDataService.TouchClient(ctx, signIn.InstanceID, signIn.ClientID)
}

type ConvertToSessionParams struct {
	Client               *model.Client
	Env                  *model.Env
	SignIn               *model.SignIn
	User                 *model.User
	PostponeCookieUpdate bool
	RotatingTokenNonce   *string
	FromTransfer         bool
}

// ConvertToSession -
func (s *Service) ConvertToSession(ctx context.Context, tx database.Tx, params ConvertToSessionParams) (*model.Session, error) {
	userSettings := usersettings.NewUserSettings(params.Env.AuthConfig.UserSettings)
	client := params.Client
	clientUpdateCols := set.New[string]()

	// If we're signing in a user that has an ended session, mark the ended one replaced.
	// Important note: the retrieval and consequent update operations are performed outside of the in-progress database transaction (tx).
	clientSessions, deadSession, err := s.findReplacedSessionForCurrentUser(ctx, params.Env.Instance, client, params.User.ID)
	if err != nil {
		return nil, fmt.Errorf("convertToSession: find possible replaced session for client %s: %w",
			client.ID, err)
	}

	if client.SignUpID.Valid &&
		(params.Env.AuthConfig.SessionSettings.SingleSessionMode || params.FromTransfer) {
		client.SignUpID = null.StringFromPtr(nil)
		clientUpdateCols.Insert(sqbmodel.ClientColumns.SignUpID)
	}

	if params.Env.AuthConfig.SessionSettings.SingleSessionMode {
		// If the session isn't going to be replaced, mark all client sessions as removed
		if deadSession == nil {
			if err := s.sessionService.RemoveAllWithoutClientTouch(ctx, clientSessions); err != nil {
				return nil, fmt.Errorf("convertToSession: updating status to removed for single-session mode failed: %w", err)
			}
		}
	} else {
		if params.SignIn.HasActor() {
			// We have multi-session and impersonation, we need to remove existing sessions of the
			// client to minimize the risk of an accident.
			// This will only occur if the actor does NOT have an active session on the same client. This is a
			// workaround to allow user impersonation to work for us Clerk (given that the dashboard instance and
			// the users dashboards are basically the same instance).
			actorToken, err := s.actorTokenRepo.FindByID(ctx, tx, params.SignIn.ActorTokenID.String)
			if err != nil {
				return nil, fmt.Errorf("convertToSession: finding actor token with id %s: %w",
					params.SignIn.ActorTokenID.String, err)
			}
			actorID, err := actorToken.ActorID()
			if err != nil {
				return nil, fmt.Errorf("convertToSession: getting actor id from actor token %v: %w",
					actorToken, err)
			}

			var actorHasActiveSessionOnClient bool
			for _, session := range clientSessions {
				if session.UserID == actorID {
					actorHasActiveSessionOnClient = true
					break
				}
			}

			if !actorHasActiveSessionOnClient {
				if err := s.sessionService.RemoveAllWithoutClientTouch(ctx, clientSessions); err != nil {
					return nil, fmt.Errorf("convertToSession: updating status of existing sessions to "+
						"removed because of multi-session and impersonation failed: %w", err)
				}
			}
		} else {
			// check if we already have an active impersonation session in the client,
			// if we do, we shouldn't allow the creation of new sessions
			for _, session := range clientSessions {
				if session.IsActive(s.clock) && session.HasActor() {
					return nil, apierror.CannotCreateSessionWhenImpersonationIsPresent()
				}
			}
		}
	}

	// retrieve externalAccount (if applicable)
	var externalAccount *model.ExternalAccount

	if params.SignIn.IdentificationID.Valid {
		externalAccount, err = s.externalAccountRepo.QueryByIdentificationID(ctx, tx, params.SignIn.IdentificationID.String)
		if err != nil {
			return nil, fmt.Errorf("convertToSession: querying external account with id %s: %w",
				params.SignIn.IdentificationID.String, err)
		}
	}

	if params.SignIn.InvitationID.Valid {
		invitation := &model.Invitation{
			Invitation: &sqbmodel.Invitation{
				ID:     params.SignIn.InvitationID.String,
				Status: constants.StatusAccepted,
			},
		}
		err := s.invitationRepo.UpdateStatus(ctx, tx, invitation)
		if err != nil {
			return nil, err
		}
	}

	var activeOrganizationID *string
	if params.SignIn.OrganizationInvitationID.Valid {
		invitation, err := s.organizationService.AcceptInvitation(ctx, tx, organizations.AcceptInvitationParams{
			InvitationID: params.SignIn.OrganizationInvitationID.String,
			UserID:       params.User.ID,
			Instance:     params.Env.Instance,
			Subscription: params.Env.Subscription,
		})
		if err != nil {
			return nil, err
		}
		activeOrganizationID = &invitation.OrganizationID
	}

	if err = s.verifyReservedIdentification(ctx, tx, params.SignIn, userSettings, params.Env.Instance.ID, params.User); err != nil {
		return nil, fmt.Errorf("convertToSession: verifying reserved identification (sign in=%s): %w", params.SignIn.ID, err)
	}

	// Finalize the unverified email verification flow, which currently applies only to instances
	// that allow unverified emails at sign-up
	if err = s.identificationService.FinalizeReVerifyFlow(ctx, tx, params.Env.Instance.ID, params.User.ID); err != nil {
		return nil, err
	}

	if externalAccount != nil {
		if err := s.externalAccountService.EnsureRefreshTokenExists(ctx, tx, externalAccount); err != nil {
			return nil, err
		}
	}

	// create session
	newUserSession, err := s.sessionService.Create(ctx, tx, sessions.CreateParams{
		AuthConfig:           params.Env.AuthConfig,
		Instance:             params.Env.Instance,
		ClientID:             client.ID,
		User:                 params.User,
		ActivityID:           params.SignIn.SessionActivityID.Ptr(),
		ExternalAccount:      externalAccount,
		ActorTokenID:         params.SignIn.ActorTokenID.Ptr(),
		ActiveOrganizationID: activeOrganizationID,
		SessionStatus:        strings.ToPtr(constants.SESSPendingActivation),
	})
	if err != nil {
		return nil, fmt.Errorf("convertToSession: creating session for (client=%s, user=%s, external account=%+v): %w",
			client.ID, params.User.ID, externalAccount, err)
	}

	// mark old session as replaced, if necessary
	if deadSession != nil {
		deadSession.ReplacementSessionID = null.StringFrom(newUserSession.ID)
		cdsSession := client_data.NewSessionFromSessionModel(deadSession)
		if err := s.clientDataService.UpdateSessionReplacementSessionID(ctx, cdsSession); err != nil {
			return nil, fmt.Errorf("convertToSession: updating replacement session id of %+v: %w",
				deadSession, err)
		}
		cdsSession.CopyToSessionModel(deadSession)
	}

	// add session_id to sign_in, and mark complete
	params.SignIn.CreatedSessionID = null.StringFrom(newUserSession.ID)
	if err = s.signInRepo.UpdateCreatedSessionID(ctx, tx, params.SignIn); err != nil {
		return nil, fmt.Errorf("convertToSession: updating created session id for %+v: %w", params.SignIn, err)
	}

	// increment successful sign-in count for instance
	err = jobs.IncrementSuccessfulSignInCount(ctx, s.gueClient, jobs.IncrementSuccessfulSignInCountArgs{
		InstanceID: params.Env.Instance.ID,
		Day:        params.SignIn.CreatedAt.UTC().Format("2006-01-02"),
	}, jobs.WithTx(tx))
	if err != nil {
		return nil, err
	}

	if params.SignIn.NewPasswordDigest.Valid {
		// user went through reset password flow
		err := s.passwordService.ChangeUserPassword(ctx, tx, password.ChangeUserPasswordParams{
			Env:                    params.Env,
			PasswordDigest:         params.SignIn.NewPasswordDigest.String,
			PasswordHasher:         hash.Bcrypt,
			SignOutOfOtherSessions: params.SignIn.SignOutOfOtherSessions,
			User:                   params.User,
			RequestingSessionID:    params.SignIn.CreatedSessionID.Ptr(),
		})
		if err != nil {
			return nil, fmt.Errorf("convertToSession: changing password for user %s (sign in=%s): %w",
				params.User.ID, params.SignIn.ID, err)
		}
	}

	// Client changes
	// - remove sign_in
	// - rotate token and cookie value on it because we have a new session
	//
	// The above updates can happen either now or in the next time there is a call by the client.
	// This behaviour is controlled by the `postponeCookieUpdate`.
	// The reason is that the convert to session might be called by a different device (i.e. in the
	// case of magic links). In this case, we need to make sure that the cookie is
	// updated by the device that started the process of session creation, in order to get the
	// updated cookie.
	if params.PostponeCookieUpdate {
		client.PostponeCookieUpdate = true
		clientUpdateCols.Insert(sqbmodel.ClientColumns.PostponeCookieUpdate)
	} else {
		if params.RotatingTokenNonce != nil {
			client.RotatingTokenNonce = null.StringFromPtr(params.RotatingTokenNonce)
			clientUpdateCols.Insert(sqbmodel.ClientColumns.RotatingTokenNonce)
		}
		if err := s.cookieService.UpdateClientCookieValue(ctx, params.Env.Instance, client); err != nil {
			return nil, fmt.Errorf("convertToSession: updating client cookie for instance=%s, client=%s: %w",
				params.Env.Instance.ID, client.ID, err)
		}

		client.SignInID = null.StringFromPtr(nil)
		clientUpdateCols.Insert(sqbmodel.ClientColumns.SignInID)
	}

	// Update the client
	cdsClient := client_data.NewClientFromClientModel(client)
	if err := s.clientDataService.UpdateClient(ctx, params.Env.Instance.ID, cdsClient, clientUpdateCols.Array()...); err != nil {
		return nil, fmt.Errorf("convertToSession: cannot update client %s columns %v: %w", client.ID, clientUpdateCols.Array(), err)
	}
	cdsClient.CopyToClientModel(client)

	if err := s.resetFailedVerificationAttempts(ctx, tx, params.Env, params.User, params.SignIn); err != nil {
		return nil, fmt.Errorf("convertToSession: updating lockout columns for user %s failed: %w", params.User.ID, err)
	}

	return newUserSession, nil
}

func (s Service) findReplacedSessionForCurrentUser(ctx context.Context, instance *model.Instance, client *model.Client, userID string) ([]*model.Session, *model.Session, error) {
	currentSessions, err := s.clientDataService.FindAllCurrentSessionsByClients(ctx, instance.ID, []string{client.ID})
	if err != nil {
		return nil, nil, fmt.Errorf("convertToSession: fetching all current sessions for client %s: %w",
			client.ID, err)
	}

	var replacedSession *model.Session
	for _, session := range currentSessions {
		if session.UserID == userID {
			replacedSession = session.ToSessionModel()
			break
		}
	}
	return client_data.ToSessionModels(currentSessions), replacedSession, nil
}

// If the user signed in with a strategy that proves they own the identifier (email, phone), then mark
// the identification as verified.
func (s Service) verifyReservedIdentification(ctx context.Context, tx database.Tx, signIn *model.SignIn, userSettings *usersettings.UserSettings, instanceID string, user *model.User) error {
	if !signIn.FirstFactorSuccessVerificationID.Valid || !userSettings.SignUp.Progressive {
		return nil
	}

	firstFactorVerification, err := s.verificationRepo.FindByIDAndInstance(ctx, tx, instanceID, signIn.FirstFactorSuccessVerificationID.String)
	if err != nil {
		return err
	}

	strategy, ok := strategies.GetStrategy(firstFactorVerification.Strategy)
	if !ok || !strategy.ProvesIdentifierOwnership() {
		return nil
	}

	// There is a chance that the identifier attached to the sign-in object, is not actually the one that the
	// user verified to be able to sign-in. For example an instance has enabled both email & phone for sign in,
	// but there is no strategy configured for phone numbers. The user can provide their phone number for sign-in,
	// although an OTP or magic link will be sent to their email address instead. That's the reason we must rely
	// on the verification's identification to verify it if applicable instead of sign-in
	if !firstFactorVerification.IdentificationID.Valid {
		return nil
	}

	firstFactorIdent, err := s.identificationRepo.FindByIDAndInstance(ctx, tx, firstFactorVerification.IdentificationID.String, instanceID)
	if err != nil {
		return err
	}

	if !firstFactorIdent.IsReserved() {
		return nil
	}

	firstFactorIdent.Status = constants.ISVerified
	firstFactorIdent.VerificationID = null.StringFrom(firstFactorVerification.ID)
	err = s.identificationRepo.Update(ctx, tx, firstFactorIdent,
		sqbmodel.IdentificationColumns.Status,
		sqbmodel.IdentificationColumns.VerificationID)
	if err != nil {
		return err
	}

	return s.identificationService.RestoreUserReservedAndPrimaryIdentifications(ctx, tx, firstFactorIdent, userSettings, instanceID, user)
}

func (s *Service) resetFailedVerificationAttempts(ctx context.Context, tx database.Tx, env *model.Env, user *model.User, signIn *model.SignIn) error {
	if signIn.HasActor() {
		return nil
	}

	err := s.userLockoutService.ResetFailedVerificationAttempts(ctx, tx, env, user)
	if err != nil {
		return err
	}

	return nil
}

func (s *Service) ConvertToSerializable(
	ctx context.Context,
	exec database.Executor,
	signIn *model.SignIn,
	userSettings *usersettings.UserSettings,
	oauthAuthorizationURL string) (*model.SignInSerializable, error) {
	signInSerializable := model.SignInSerializable{SignIn: signIn}
	status := signIn.Status(s.clock)

	var err error

	// populate Identification, User & FirstFactors
	if signIn.IdentificationID.Valid {
		signInSerializable.Identification, err = s.identificationRepo.FindByID(ctx, exec, signIn.IdentificationID.String)
		if err != nil {
			return nil, clerkerrors.WithStacktrace(
				"signIn/convertToSerializable: fetching identification %s: %w", signIn.IdentificationID.String, err)
		}

		user, err := s.userRepo.FindByID(ctx, exec, signInSerializable.Identification.UserID.String)
		if err != nil {
			return nil, clerkerrors.WithStacktrace("signIn/convertToSerializable: fetching user %s: %w", signInSerializable.Identification.UserID.String, err)
		}

		signInSerializable.User, err = s.serializableService.ConvertUser(ctx, exec, userSettings, user)
		if err != nil {
			return nil, clerkerrors.WithStacktrace("signIn/convertToSerializable: serializing user %+v: %w", user, err)
		}
	}

	if status != constants.SignInComplete && signInSerializable.User != nil {
		firstFactorIdentifications, err := s.identificationRepo.FindAllFirstFactorsByUser(ctx, exec, signInSerializable.User.ID)
		if err != nil {
			return nil, fmt.Errorf("signIn/convertToSerializable: fetching first factor identifications for user %s: %w",
				signInSerializable.User.ID, err)
		}

		firstFactors := userSettings.FirstFactors()
		signInSerializable.FirstFactors, err = s.Factors(ctx, exec, signIn, signInSerializable.User.User, firstFactorIdentifications, firstFactors)
		if err != nil {
			return nil, fmt.Errorf(
				"signIn/convertToSerializable: fetching first factors for (%+v, %+v, %+v): %w",
				signIn, signInSerializable.User, firstFactors, err)
		}
	} else {
		signInSerializable.FirstFactors = nil
	}

	// populate FactorOne/FactorTwo strategies along with their
	// corresponding Verifications
	var verificationID string
	if signIn.FirstFactorSuccessVerificationID.Valid {
		verificationID = signIn.FirstFactorSuccessVerificationID.String
	} else if signIn.FirstFactorCurrentVerificationID.Valid {
		verificationID = signIn.FirstFactorCurrentVerificationID.String
	}
	if verificationID != "" {
		signInSerializable.FirstFactorVerification, err = s.verificationService.VerificationWithStatus(ctx, exec, verificationID)
		if err != nil {
			return nil, fmt.Errorf("signIn/convertToSerializable: fetching verification with status of (%s, %s): %w",
				verificationID, signIn.InstanceID, err)
		}

		if oauthAuthorizationURL != "" {
			signInSerializable.FirstFactorVerification.ExternalAuthorizationURL = null.StringFrom(oauthAuthorizationURL)
		}
	}

	// populate SecondFactors
	//
	// - if status is needs_second_factor, the user is guaranteed to be valid.
	// - currently the only second factor option is a phone_code
	if status == constants.SignInNeedsSecondFactor && signInSerializable.User != nil {
		secondFactorIdentifications, err := s.identificationRepo.FindAllSecondFactorsByUser(ctx, exec, signInSerializable.User.ID)
		if err != nil {
			return nil, fmt.Errorf("signIn/convertToSerializable: fetching second factor identifications for user %s: %w",
				signInSerializable.User.ID, err)
		}

		secondFactors := userSettings.SecondFactors()
		signInSerializable.SecondFactors, err = s.Factors(ctx, exec, signIn, signInSerializable.User.User, secondFactorIdentifications, secondFactors)
		if err != nil {
			return nil, fmt.Errorf(
				"signIn/convertToSerializable: fetching second factors for (%+v, %+v, %+v): %w",
				signIn, signInSerializable.User, secondFactors, err)
		}
	} else {
		signInSerializable.SecondFactors = nil
	}

	verificationID = ""
	if signIn.SignIn.SecondFactorSuccessVerificationID.Valid {
		verificationID = signIn.SecondFactorSuccessVerificationID.String
	} else if signIn.SignIn.SecondFactorCurrentVerificationID.Valid {
		verificationID = signIn.SecondFactorCurrentVerificationID.String
	}
	if verificationID != "" {
		signInSerializable.SecondFactorVerification, err = s.verificationService.VerificationWithStatus(ctx, exec, verificationID)
		if err != nil {
			return nil, clerkerrors.WithStacktrace(
				"signIn/convertToSerializable: fetching verification with status of (%s, %s): %w",
				verificationID, signIn.InstanceID, err)
		}
	}

	if signInSerializable.User == nil {
		signInSerializable.SupportedFirstFactors = firstFactorStrategiesNotRequiringIdentifierUser(userSettings)
	} else {
		signInSerializable.SupportedFirstFactors = signInSerializable.FirstFactors
	}

	return &signInSerializable, nil
}

func firstFactorStrategiesNotRequiringIdentifierUser(userSettings *usersettings.UserSettings) []model.SignInFactor {
	expandedFactors := make([]model.SignInFactor, 0)
	for _, firstFactor := range userSettings.FirstFactors().Array() {
		strategy, ok := strategies.GetStrategy(firstFactor)
		if ok && strategy.SignInNeedsIdentifiedUser() {
			continue
		}

		expandedFactors = append(expandedFactors, model.SignInFactor{Strategy: firstFactor})
	}
	return expandedFactors
}

// Factors returns a list of sign in factors based on the user's identifications and the allowed
// verification strategies of the given factor.
func (s *Service) Factors(
	ctx context.Context,
	exec database.Executor,
	signIn *model.SignIn,
	user *model.User,
	identifications []*model.Identification,
	allowedFactorStrategies set.Set[string],
) ([]model.SignInFactor, error) {
	expandedFactors := make([]model.SignInFactor, 0)
	if allowedFactorStrategies.Contains(constants.VSPassword) && user.PasswordDigest.Valid {
		expandedFactors = append(expandedFactors, model.SignInFactor{
			Strategy: constants.VSPassword,
		})
	}

	if allowedFactorStrategies.Contains(constants.VSTOTP) {
		totpExists, err := s.totpRepo.ExistsVerifiedByUser(ctx, exec, user.ID)
		if err != nil {
			return nil, fmt.Errorf("factors: totp record exists for user %s: %w", user.ID, err)
		}
		if totpExists {
			expandedFactors = append(expandedFactors, model.SignInFactor{
				Strategy: constants.VSTOTP,
			})
		}
	}

	if allowedFactorStrategies.Contains(constants.VSBackupCode) {
		backupCodeExists, err := s.backupCodeRepo.ExistsByUser(ctx, exec, user.ID)
		if err != nil {
			return nil, fmt.Errorf("factors: backup code record exists for user %s: %w", user.ID, err)
		}
		if backupCodeExists {
			expandedFactors = append(expandedFactors, model.SignInFactor{
				Strategy: constants.VSBackupCode,
			})
		}
	}

	var err error
	var addPasskeyFactor bool
	identificationsByID := make(map[string]*model.Identification, len(identifications))
	for _, identification := range identifications {
		identificationsByID[identification.ID] = identification

		if identification.IsEmailAddress() {
			if allowedFactorStrategies.Contains(constants.VSEmailCode) {
				expandedFactors, err = s.createAndAppendFactor(expandedFactors, identification, user, constants.VSEmailCode, signIn.IdentificationID.String)
				if err != nil {
					return nil, fmt.Errorf("firstFactors: creating and append email code factor for %+v: %w", identification, err)
				}
			}
			if allowedFactorStrategies.Contains(constants.VSEmailLink) {
				expandedFactors, err = s.createAndAppendFactor(expandedFactors, identification, user, constants.VSEmailLink, signIn.IdentificationID.String)
				if err != nil {
					return nil, fmt.Errorf("firstFactors: creating and append email link factor for %+v: %w", identification, err)
				}
			}
		} else if identification.IsPhoneNumber() && allowedFactorStrategies.Contains(constants.VSPhoneCode) {
			expandedFactors, err = s.createAndAppendFactor(expandedFactors, identification, user, constants.VSPhoneCode, signIn.IdentificationID.String)
			if err != nil {
				return nil, fmt.Errorf("firstFactors: creating and append phone code factor for %+v: %w", identification, err)
			}
		} else if identification.IsWeb3Wallet() && allowedFactorStrategies.Contains(constants.VSWeb3MetamaskSignature) {
			expandedFactors, err = s.createAndAppendFactor(expandedFactors, identification, user, constants.VSWeb3MetamaskSignature, signIn.IdentificationID.String)
			if err != nil {
				return nil, fmt.Errorf("firstFactors: creating and append web3 wallet factor for %+v: %w", identification, err)
			}
		} else if identification.IsOAuth() && allowedFactorStrategies.Contains(identification.Type) {
			expandedFactors, err = s.createAndAppendFactor(expandedFactors, identification, user, identification.Type, signIn.IdentificationID.String)
			if err != nil {
				return nil, fmt.Errorf("firstFactors: creating and append OAuth factor for %+v: %w", identification, err)
			}
		} else if identification.IsPasskey() && allowedFactorStrategies.Contains(constants.VSPasskey) {
			addPasskeyFactor = true
		}
	}

	// passkey sign ins are not tied to a specific identification
	// append the passkey factor if at least one passkey identification exists
	if addPasskeyFactor {
		expandedFactors = append(expandedFactors, model.SignInFactor{
			Strategy: constants.VSPasskey,
		})
	}

	currentVersion := clerkjs_version.FromContext(ctx)
	versionWithResetPassword := cenv.Get(cenv.ResetPasswordClerkJSVersion)
	// we include the reset password code factor if the following conditions
	// are met:
	// * is in the allowed first factor strategies (i.e. passwords are enabled)
	// * the request is made from one of the newer ClerkJS versions
	// * user already has a password
	if isResetPasswordStrategyAllowed(allowedFactorStrategies) &&
		!versions.IsBefore(currentVersion, versionWithResetPassword, true) &&
		user.PasswordDigest.Valid {
		identificationToUseForResetPassword, strategy, found := determineResetPasswordIdentificationAndStrategy(signIn, user, identificationsByID)
		if found {
			expandedFactors, err = s.createAndAppendFactor(expandedFactors, identificationToUseForResetPassword, user, strategy, signIn.IdentificationID.String)
			if err != nil {
				return nil, fmt.Errorf("firstFactors: creating and append reset password code factor for %s with strategy %s: %w",
					identificationToUseForResetPassword.ID, strategy, err)
			}
		}
	}

	return expandedFactors, nil
}

func isResetPasswordStrategyAllowed(allowedFactorStrategies set.Set[string]) bool {
	return allowedFactorStrategies.Contains(constants.VSResetPasswordEmailCode) ||
		allowedFactorStrategies.Contains(constants.VSResetPasswordPhoneCode)
}

// determineResetPasswordIdentification returns the user's identification that will be used for
// communication during reset password flow along with the strategy.
// Initially, we'll try to use the identification that was used in sign in. This can only be used if it's
// a communication identification, i.e. email address or phone number.
// Otherwise, we'll use either the user's primary email address or primary phone number.
// If none of the above is applicable, we return `nil` which means that no suitable identifications were
// found.
func determineResetPasswordIdentificationAndStrategy(signIn *model.SignIn, user *model.User, identifications map[string]*model.Identification) (*model.Identification, string, bool) {
	// first, try to use the identifier that the user entered during sign in if it can be used for communication
	if signInIdentification := identifications[signIn.IdentificationID.String]; signInIdentification != nil {
		if signInIdentification.IsEmailAddress() {
			return signInIdentification, constants.VSResetPasswordEmailCode, true
		} else if signInIdentification.IsPhoneNumber() {
			return signInIdentification, constants.VSResetPasswordPhoneCode, true
		}
	}

	// otherwise, use user's primary email address
	if primaryEmailAddress := identifications[user.PrimaryEmailAddressID.String]; primaryEmailAddress != nil {
		return primaryEmailAddress, constants.VSResetPasswordEmailCode, true
	}

	// finally use user's primary phone number
	// if it doesn't exist, we return `nil` which denotes that no suitable identifications were found
	if primaryPhoneNumber := identifications[user.PrimaryPhoneNumberID.String]; primaryPhoneNumber != nil {
		return primaryPhoneNumber, constants.VSResetPasswordPhoneCode, true
	}
	return nil, "", false
}

func (s *Service) createAndAppendFactor(factors []model.SignInFactor, identification *model.Identification, user *model.User, factorType, signInIdentificationID string) ([]model.SignInFactor, error) {
	factor := model.SignInFactor{
		Strategy: factorType,
		Primary:  identification.IsUserPrimary(user),
		Default:  identification.DefaultSecondFactor,
	}

	var identifier *string
	if identification.IsEmailAddress() {
		identifier = identification.EmailAddress()
		factor.EmailAddressID = &identification.ID
	} else if identification.IsPhoneNumber() {
		identifier = identification.PhoneNumber()
		factor.PhoneNumberID = &identification.ID
	} else if identification.IsWeb3Wallet() {
		identifier = identification.Web3Wallet()
		factor.Web3WalletID = &identification.ID
	}

	if identifier != nil {
		if signInIdentificationID != identification.ID {
			maskedIdentifier, err := strings.MaskIdentifier(*identifier, identification.Type)
			if err != nil {
				return nil, fmt.Errorf("appendFactor: masking identifier %s: %w", *identifier, err)
			}

			factor.SafeIdentifier = maskedIdentifier
		} else {
			factor.SafeIdentifier = identifier
		}
	}
	return append(factors, factor), nil
}

// ResetReVerificationState clears the verification state and errors of the
// identification that was used during the sign-in process.
func (s *Service) ResetReVerificationState(ctx context.Context, tx database.Tx, signIn *model.SignIn) error {
	var ident *model.Identification
	var err error

	if signIn.ToLinkIdentificationID.Valid {
		ident, err = s.identificationRepo.FindByID(ctx, tx, signIn.ToLinkIdentificationID.String)
		if err != nil {
			return err
		}
	} else {
		ident, err = s.identificationRepo.FindByID(ctx, tx, signIn.IdentificationID.String)
		if err != nil {
			return err
		}
	}

	// Reset the verification state of the identification
	if ident.RequiresVerification.Valid {
		ident.RequiresVerification = null.BoolFromPtr(nil)
		if err = s.identificationRepo.Update(ctx, tx, ident, sqbmodel.IdentificationColumns.RequiresVerification); err != nil {
			return err
		}
	}

	// Clean up any verification errors, for example if the user connected the account
	// at some point with unverified email address
	return s.verificationService.ClearErrors(ctx, tx, ident.VerificationID)
}

// LinkIdentificationToUser completes the process of linking an external account identification
// to an existing user during the sign-in process, while also cleaning up the sign-in and verification object.
func (s *Service) LinkIdentificationToUser(ctx context.Context, tx database.Tx, signIn *model.SignIn, userID string) error {
	if !signIn.ToLinkIdentificationID.Valid {
		return nil
	}

	extAccIdent, err := s.identificationRepo.FindByID(ctx, tx, signIn.ToLinkIdentificationID.String)
	if err != nil {
		return err
	}

	// complete linking by setting the stored ID as target ID
	extAccIdent.TargetIdentificationID = null.StringFrom(signIn.IdentificationID.String)

	// assign user ID to external account, if not already set.
	if !extAccIdent.UserID.Valid {
		extAccIdent.UserID = null.StringFrom(userID)
	}

	if err = s.identificationRepo.Update(ctx, tx, extAccIdent); err != nil {
		return err
	}

	// Clean up the sign in object
	signIn.ToLinkIdentificationID = null.StringFromPtr(nil)
	return s.signInRepo.Update(ctx, tx, signIn, sqbmodel.SignInColumns.ToLinkIdentificationID)
}
