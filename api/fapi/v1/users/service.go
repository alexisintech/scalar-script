package users

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/comms"
	"clerk/api/shared/events"
	"clerk/api/shared/identifications"
	"clerk/api/shared/organizations"
	"clerk/api/shared/orgdomain"
	"clerk/api/shared/pagination"
	"clerk/api/shared/password"
	"clerk/api/shared/phone_numbers"
	"clerk/api/shared/restrictions"
	"clerk/api/shared/serializable"
	sharedstrategies "clerk/api/shared/strategies"
	"clerk/api/shared/user_profile"
	"clerk/api/shared/users"
	"clerk/api/shared/validators"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/backup_codes"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requesting_session"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/emailaddress"
	"clerk/pkg/hash"
	"clerk/pkg/oauth"
	"clerk/pkg/phonenumber"
	"clerk/pkg/totp"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/pkg/usersettings/clerk/strategies"
	usersettingsmodel "clerk/pkg/usersettings/model"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"
	"clerk/utils/param"
	"clerk/utils/validate"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	deps  clerk.Deps
	clock clockwork.Clock
	db    database.Database

	// services
	commsService          *comms.Service
	eventService          *events.Service
	identificationService *identifications.Service
	orgDomainService      *orgdomain.Service
	organizationService   *organizations.Service
	passwordService       *password.Service
	phoneNumbersService   *phone_numbers.Service
	restrictionService    *restrictions.Service
	serializableService   *serializable.Service
	userService           *users.Service
	userProfileService    *user_profile.Service
	validatorService      *validators.Service
	verificationService   *verifications.Service
	clientDataService     *client_data.Service

	// repositories
	backupCodeRepo             *repository.BackupCode
	externalAccountRepo        *repository.ExternalAccount
	imageRepo                  *repository.Images
	organizationRepo           *repository.Organization
	organizationMemberRepo     *repository.OrganizationMembership
	orgMemberRequestRepo       *repository.OrganizationMembershipRequest
	organizationInvitationRepo *repository.OrganizationInvitation
	organizationSuggestionRepo *repository.OrganizationSuggestion
	permissionRepo             *repository.Permission
	totpRepo                   *repository.TOTP
	userRepo                   *repository.Users
	identificationRepo         *repository.Identification
	verificationRepo           *repository.Verification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		deps:                       deps,
		clock:                      deps.Clock(),
		db:                         deps.DB(),
		commsService:               comms.NewService(deps),
		eventService:               events.NewService(deps),
		identificationService:      identifications.NewService(deps),
		orgDomainService:           orgdomain.NewService(deps.Clock()),
		organizationService:        organizations.NewService(deps),
		passwordService:            password.NewService(deps),
		phoneNumbersService:        phone_numbers.NewService(deps),
		restrictionService:         restrictions.NewService(deps.EmailQualityChecker()),
		serializableService:        serializable.NewService(deps.Clock()),
		userService:                users.NewService(deps),
		userProfileService:         user_profile.NewService(deps.Clock()),
		validatorService:           validators.NewService(),
		verificationService:        verifications.NewService(deps.Clock()),
		clientDataService:          client_data.NewService(deps),
		backupCodeRepo:             repository.NewBackupCode(),
		imageRepo:                  repository.NewImages(),
		externalAccountRepo:        repository.NewExternalAccount(),
		organizationRepo:           repository.NewOrganization(),
		organizationMemberRepo:     repository.NewOrganizationMembership(),
		orgMemberRequestRepo:       repository.NewOrganizationMembershipRequest(),
		organizationInvitationRepo: repository.NewOrganizationInvitation(),
		organizationSuggestionRepo: repository.NewOrganizationSuggestion(),
		permissionRepo:             repository.NewPermission(),
		totpRepo:                   repository.NewTOTP(),
		userRepo:                   repository.NewUsers(),
		identificationRepo:         repository.NewIdentification(),
		verificationRepo:           repository.NewVerification(),
	}
}

// SetRequestingUser loads the user that corresponds to the given session id into the context
func (s *Service) SetRequestingUser(ctx context.Context, userSessionID *string) (context.Context, apierror.Error) {
	env := environment.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	var sess *model.Session
	if env.AuthConfig.SessionSettings.SingleSessionMode {
		// You're not authenticated as anyone. Please prove you are who you say you are.
		activeSessions, err := s.clientDataService.FindAllClientSessions(ctx, env.Instance.ID, client.ID, &client_data.SessionFilterParams{
			ActiveOnly: true,
		})
		if err != nil {
			return ctx, apierror.Unexpected(err)
		}

		if len(activeSessions) > 0 {
			// if we have sessions, grab the first one...otherwise we'll throw an unauthenticated error down the line
			sess = activeSessions[0].ToSessionModel()
		}
	} else {
		// Your request is missing a Clerk-Session. Please tell us who you are.
		if userSessionID == nil {
			return ctx, apierror.InvalidAuthentication()
		}

		// You're not authenticated as Clerk-Session. Please prove you are who you say you are.
		activeSessions, err := s.clientDataService.FindAllClientSessions(ctx, env.Instance.ID, client.ID, &client_data.SessionFilterParams{
			ActiveOnly: true,
		})
		if err != nil {
			return ctx, apierror.Unexpected(err)
		}

		for i := range activeSessions {
			userSession := activeSessions[i].ToSessionModel()
			if userSession.ID == *userSessionID {
				sess = userSession
				break
			}
		}
	}

	// You're not authenticated as Clerk-User-Id. Please prove you are who you say you are.
	if sess == nil || sess.GetStatus(s.clock) != constants.SESSActive {
		return ctx, apierror.InvalidAuthentication()
	}

	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, sess.UserID, env.Instance.ID)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	} else if user == nil {
		return ctx, apierror.UserNotFound(sess.UserID)
	}

	ctx = requesting_session.NewContext(ctx, sess)
	ctx = requesting_user.NewContext(ctx, user)

	log.AddToLogLine(ctx, log.SessionID, sess.ID)
	log.AddToLogLine(ctx, log.UserID, user.ID)

	actorID, err := sess.ActorID()
	if err != nil {
		return ctx, apierror.Unexpected(err)
	}
	if actorID != nil {
		log.AddToLogLine(ctx, log.ActorID, actorID)
	}

	return ctx, nil
}

// Read returns the user loaded in the request's context wrapped along the current client
func (s *Service) Read(ctx context.Context, user *model.User) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	userSerializable, err := s.serializableService.ConvertUser(ctx, s.db, userSettings, user)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.UserToClientAPI(ctx, userSerializable), nil
}

// DeleteProfileImage clears the users profile_image_url.
func (s *Service) DeleteProfileImage(ctx context.Context, userID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	return s.userService.DeleteProfileImage(ctx, userID)
}

// ListIdentifications returns all identifications of the given type associated with the given user
func (s *Service) ListIdentifications(ctx context.Context, userID, identificationType string) ([]interface{}, apierror.Error) {
	env := environment.FromContext(ctx)

	identifications, err := s.identificationRepo.FindAllByUserAndType(ctx, s.db, env.Instance.ID, userID, identificationType)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]interface{}, len(identifications))
	for i, ident := range identifications {
		response, err := s.toIdentificationResponse(ctx, ident)
		if err != nil {
			return nil, err
		}

		responses[i] = response
	}
	return responses, nil
}

// DeleteIdentification attempts to delete the identification with the provided
// identificationID for the user specified with userID.
// Touches the user upon successful email deletion.
// Identifications with linked parent identifications cannot be deleted.
func (s *Service) DeleteIdentification(ctx context.Context, user *model.User, identificationID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	var deletedObject *serialize.DeletedObjectResponse
	var apiErr apierror.Error
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		deletedObject, apiErr = s.deleteIdentification(ctx, tx, user, identificationID)
		return apiErr != nil, apiErr
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return deletedObject, nil
}

func (s *Service) DeleteEmailAddress(ctx context.Context, user *model.User, emailAddressID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	var previousPrimaryEmailAddress *string
	var err error
	if user.PrimaryEmailAddressID.Valid && user.PrimaryEmailAddressID.String == emailAddressID {
		// We're attempting to delete the primary email address.
		// It's possible that another email address will become the primary. Store the previous primary.
		previousPrimaryEmailAddress, err = s.userProfileService.GetPrimaryEmailAddress(ctx, s.db, user)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	var deletedObject *serialize.DeletedObjectResponse
	var apiErr apierror.Error
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		deletedObject, apiErr = s.deleteIdentification(ctx, tx, user, emailAddressID)
		if apiErr != nil {
			return true, apiErr
		}

		err := s.userService.NotifyPrimaryEmailChanged(ctx, tx, user, env, previousPrimaryEmailAddress)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return deletedObject, nil
}

func (s *Service) deleteIdentification(
	ctx context.Context,
	tx database.Tx,
	user *model.User,
	identificationID string,
) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	ident, err := s.identificationRepo.QueryByIDAndUser(ctx, tx, env.Instance.ID, identificationID, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if ident == nil {
		return nil, apierror.IdentificationNotFound(identificationID)
	}

	deletedObject, apiErr := s.identificationService.Delete(ctx, tx, env.Instance, userSettings, user, ident)
	if apiErr != nil {
		return nil, apiErr
	}

	if _, err := s.cleanupBackupCodes(ctx, tx, userSettings, user); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return deletedObject, nil
}

// ReadIdentification returns the email address or phone number loaded into the context
func (s *Service) ReadIdentification(ctx context.Context, userID, identificationID string) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)

	identification, err := s.fetchIdentification(ctx, identificationID, env.Instance.ID, userID)
	if err != nil {
		return nil, err
	}

	return s.toIdentificationResponse(ctx, identification)
}

// CreateEmailAddress creates a new email address for given user
func (s *Service) CreateEmailAddress(ctx context.Context, user *model.User, emailAddress string) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	emailAddressAttribute := userSettings.GetAttribute(names.EmailAddress)

	if !emailAddressAttribute.Base().Enabled {
		return nil, apierror.FormUnknownParameter(param.EmailAddress.Name)
	}

	var apiErr apierror.Error
	emailAddress, apiErr = emailAddressAttribute.Sanitize(emailAddress, param.EmailAddress.Name)
	if apiErr != nil {
		return nil, apiErr
	}

	// Ensure it's not a test email address
	if emailaddress.IsTest(emailAddress) && !env.AuthConfig.TestMode {
		return nil, apierror.FormInvalidEmailAddress(param.EmailAddress.Name)
	}

	subErr, unexpectedErr := s.validatorService.ValidateEmailAddress(ctx, s.db, emailAddress, env.Instance.ID, &user.ID, !emailAddressAttribute.Base().VerifyAtSignUp, param.EmailAddress.Name)
	if unexpectedErr != nil {
		return nil, apierror.Unexpected(unexpectedErr)
	} else if subErr != nil {
		return nil, subErr
	}

	// Check instance restrictions
	res, err := s.restrictionService.Check(
		ctx,
		s.db,
		restrictions.Identification{
			Identifier:          emailAddress,
			CanonicalIdentifier: emailaddress.Canonical(emailAddress),
			Type:                constants.ITEmailAddress,
		},
		restrictions.Settings{
			Restrictions: usersettingsmodel.Restrictions{
				BlockEmailSubaddresses:      userSettings.Restrictions.BlockEmailSubaddresses,
				IgnoreDotsForGmailAddresses: userSettings.Restrictions.IgnoreDotsForGmailAddresses,
				BlockDisposableEmailDomains: userSettings.Restrictions.BlockDisposableEmailDomains,
			},
		},
		env.Instance.ID,
	)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if res.Blocked || !res.Allowed {
		return nil, apierror.IdentifierNotAllowedAccess(emailAddress)
	}

	canonicalIdentifier := emailaddress.Canonical(emailAddress)
	createIdentificationData := identifications.CreateIdentificationData{
		InstanceID:          env.Instance.ID,
		UserID:              &user.ID,
		Identifier:          emailAddress,
		CanonicalIdentifier: &canonicalIdentifier,
		Type:                constants.ITEmailAddress,
	}
	identification, apiErr := s.createIdentification(ctx, createIdentificationData, user, env.Instance, env.AuthConfig)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.toIdentificationResponse(ctx, identification)
}

// CreatePhoneNumber creates a new phone number for the given user
func (s *Service) CreatePhoneNumber(ctx context.Context, user *model.User, phoneNumber string, reserveForSecondFactor *bool) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	phoneNumberAttribute := userSettings.GetAttribute(names.PhoneNumber)

	if !phoneNumberAttribute.Base().Enabled {
		return nil, apierror.FormUnknownParameter(param.PhoneNumber.Name)
	}

	var apiErr apierror.Error
	phoneNumber, apiErr = phoneNumberAttribute.Sanitize(phoneNumber, param.PhoneNumber.Name)
	if apiErr != nil {
		return nil, apiErr
	}

	// Ensure it's not a test phone number
	if model.IsTestPhoneIdentifier(phoneNumber) && !env.AuthConfig.TestMode {
		return nil, apierror.FormInvalidPhoneNumber(param.PhoneNumber.Name)
	}

	isBlocked, iso3166 := phonenumber.IsCountryBlocked(env.Instance, phoneNumber)
	if isBlocked {
		return nil, apierror.BlockedCountry(iso3166)
	}

	// Ensure the identifier is unique
	validationErr, unexpectedErr := s.validatorService.ValidatePhoneNumber(ctx, s.db, phoneNumber, env.Instance.ID, &user.ID, !phoneNumberAttribute.Base().VerifyAtSignUp, param.PhoneNumber.Name)
	if unexpectedErr != nil {
		return nil, apierror.Unexpected(unexpectedErr)
	} else if validationErr != nil {
		return nil, validationErr
	}

	exists, err := s.identificationRepo.ExistsByIdentifierAndUser(ctx, s.db, phoneNumber, constants.ITPhoneNumber, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if exists {
		return nil, apierror.FormIdentifierExists(param.PhoneNumber.Name)
	}

	createIdentificationData := identifications.CreateIdentificationData{
		InstanceID: env.Instance.ID,
		UserID:     &user.ID,
		Identifier: phoneNumber,
		Type:       constants.ITPhoneNumber,
	}
	if reserveForSecondFactor != nil {
		createIdentificationData.ReserveForSecondFactor = *reserveForSecondFactor
	}

	identification, apiErr := s.createIdentification(ctx, createIdentificationData, user, env.Instance, env.AuthConfig)
	if apiErr != nil {
		return nil, apiErr
	}
	return s.toIdentificationResponse(ctx, identification)
}

// CreateWeb3Wallet creates a new web3 wallet for given user
func (s Service) CreateWeb3Wallet(ctx context.Context, web3Wallet string) (*serialize.Web3WalletResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	web3WalletAttribute := userSettings.GetAttribute(names.Web3Wallet)

	if !userSettings.GetAttribute(names.Web3Wallet).Base().Enabled {
		return nil, apierror.FormUnknownParameter(param.Web3Wallet.Name)
	}

	var apiErr apierror.Error
	web3Wallet, apiErr = web3WalletAttribute.Sanitize(web3Wallet, param.Web3Wallet.Name)
	if apiErr != nil {
		return nil, apiErr
	}

	apiErr, unexpectedErr := s.validatorService.ValidateWeb3Wallet(ctx, s.db, web3Wallet, env.Instance.ID)
	if unexpectedErr != nil {
		return nil, apierror.Unexpected(unexpectedErr)
	} else if apiErr != nil {
		return nil, apiErr
	}

	exists, err := s.identificationRepo.ExistsByIdentifierAndUser(ctx, s.db, web3Wallet, constants.ITWeb3Wallet, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if exists {
		return nil, apierror.FormIdentifierExists(param.Web3Wallet.Name)
	}

	createIdentificationData := identifications.CreateIdentificationData{
		InstanceID: env.Instance.ID,
		UserID:     &user.ID,
		Identifier: web3Wallet,
		Type:       constants.ITWeb3Wallet,
	}
	identification, apiErr := s.createIdentification(ctx, createIdentificationData, user, env.Instance, env.AuthConfig)
	if apiErr != nil {
		return nil, apiErr
	}

	response, apiErr := s.toIdentificationResponse(ctx, identification)
	if apiErr != nil {
		return nil, apiErr
	}

	return response.(*serialize.Web3WalletResponse), nil
}

// UpdatePhoneNumber updates the phone number with the given properties
func (s *Service) UpdatePhoneNumber(ctx context.Context, user *model.User, phoneNumberID string, updateForm *phone_numbers.UpdateForMFAForm) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	var phoneNumber *model.Identification
	var backupCodes []string
	var performedUpdate bool
	var err error
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		phoneNumber, backupCodes, performedUpdate, err = s.phoneNumbersService.UpdateForMFA(ctx, tx, user, phoneNumberID, updateForm)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIError := apierror.As(txErr); isAPIError {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	// send event if something changed
	if performedUpdate {
		if err := s.sendUserUpdatedEvent(ctx, s.db, env.Instance, userSettings, user); err != nil {
			return true, apierror.Unexpected(
				fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err),
			)
		}
	}

	identificationSerializable, err := s.serializableService.ConvertIdentification(ctx, s.db, phoneNumber)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.IdentificationPhoneNumberWithBackupCodes(identificationSerializable, backupCodes), nil
}

type ConnectOAuthAccountForm struct {
	Strategy                  string
	RedirectURL               string
	ActionCompleteRedirectURL *string
	AdditionalScopes          []string
	Origin                    string
	User                      *model.User
	ForceConsentScreen        bool
}

// ConnectOAuthAccount prepares the flow to connect an oauth account with the requesting user
// We create a placeholder verification, an external account and an unverified oauth identification, so that we
// make the flow stateful. We use them to attach any potential errors after flow finishes to the verification.
// This way the frontend can pick them up and display them to the end user.
func (s *Service) ConnectOAuthAccount(ctx context.Context, params *ConnectOAuthAccountForm) (*serialize.ExternalAccountResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	providerID := params.Strategy

	// Check if we support the requested OAuth provider
	if !oauth.ProviderExists(providerID) {
		return nil, apierror.UnsupportedOauthProvider(providerID)
	}

	// Check if the requested OAuth provider is enabled by the instance
	if !userSettings.EnabledSocialStrategies().Contains(providerID) {
		return nil, apierror.OAuthProviderNotEnabled(providerID)
	}

	// Make sure the user hasn't already connected an external account of the same OAuth provider that
	// they are requesting. We only support a single external account per provider at the current time.
	existingAccount, err := s.externalAccountRepo.QueryVerifiedByUserIDAndProviderAndInstance(ctx, s.db, params.User.ID, providerID, params.User.InstanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	isReVerificationNeeded, apiErr := s.resolveExternalAccountConflict(ctx, providerID, existingAccount)
	if apiErr != nil {
		return nil, apiErr
	}

	preparer := sharedstrategies.NewOAuthPreparer(s.clock, env, sharedstrategies.OAuthPrepareForm{
		Strategy:                  providerID,
		RedirectURL:               params.RedirectURL,
		ActionCompleteRedirectURL: params.ActionCompleteRedirectURL,
		Origin:                    params.Origin,
		SourceType:                constants.OSTOAuthConnect,
		SourceID:                  params.User.ID,
		ClientID:                  client.ID,
		AdditionalScopes:          params.AdditionalScopes,
		ForceConsentScreen:        isReVerificationNeeded,
	})

	var externalAccount *model.ExternalAccount
	var verificationWithStatus *model.VerificationWithStatus
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// we must delete the old unverified oauth identifications (along with
		// external account and verification). A user can only have a single
		// unverified oauth identification per provider. Also, this protects the
		// user for connecting the same account a second time, as the old
		// verification will no longer be present and won't be able to complete
		// the flow on OAuth callback endpoint.
		toBeDeleted, err := s.identificationRepo.QueryIDsUnverifiedByUserAndType(ctx, tx, params.User.ID, providerID)
		if err != nil {
			return true, err
		}

		verification, err := preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		identification := &model.Identification{Identification: &sqbmodel.Identification{
			InstanceID:     env.Instance.ID,
			UserID:         null.StringFrom(params.User.ID),
			Type:           providerID,
			VerificationID: null.StringFrom(verification.ID),
			Status:         constants.ISNotSet,
		}}

		if err = s.identificationRepo.Insert(ctx, tx, identification); err != nil {
			return true, err
		}

		externalAccount = &model.ExternalAccount{ExternalAccount: &sqbmodel.ExternalAccount{
			InstanceID:       env.Instance.ID,
			IdentificationID: identification.ID,
			Provider:         providerID,
		}}

		if err = s.externalAccountRepo.Insert(ctx, tx, externalAccount); err != nil {
			return true, err
		}

		verification.IdentificationID = null.StringFrom(identification.ID)
		if err = s.verificationRepo.UpdateIdentificationID(ctx, tx, verification); err != nil {
			return true, err
		}

		identification.ExternalAccountID = null.StringFrom(externalAccount.ID)
		if err = s.identificationRepo.UpdateExternalAccountID(ctx, tx, identification); err != nil {
			return true, err
		}

		status, err := s.verificationService.Status(ctx, tx, verification)
		if err != nil {
			return true, err
		}

		// NOTE: we delete those after we inserted the new records above
		// (identification, external_account), to avoid a
		// deadlock between this tx and a concurrent one from
		// api/shared/identifications.Service.Delete.
		err = s.identificationRepo.DeleteByIDs(ctx, tx, toBeDeleted)
		if err != nil {
			return true, err
		}

		verificationWithStatus = &model.VerificationWithStatus{Verification: verification, Status: status}

		if err = s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, params.User); err != nil {
			return true, fmt.Errorf("user/ConnectOAuthAccount: send user updated event for (%+v, %+v): %w", params.User, env.Instance.ID, err)
		}

		return false, nil
	})
	if txErr != nil {
		if errors.Is(txErr, sharedstrategies.ErrFailedExchangeCredentialsOAuth1) {
			return nil, apierror.MisconfiguredOAuthProvider()
		} else if errors.Is(txErr, sharedstrategies.ErrSharedCredentialsNotAvailable) {
			return nil, apierror.OAuthSharedCredentialsNotSupported()
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.ExternalAccount(ctx, externalAccount, verificationWithStatus), nil
}

type reauthorizeOAuthAccountParams struct {
	AdditionalScopes          []string
	RedirectURL               string
	ActionCompleteRedirectURL *string
	ID                        string
	Origin                    string
	User                      *model.User
}

// ReauthorizeOAuthAccount initiates an OAuth authorization flow for an existing
// external account.
//
// This can be used, for example, to request additional scopes
// from an already-registered user.
func (s *Service) ReauthorizeOAuthAccount(ctx context.Context, params *reauthorizeOAuthAccountParams) (*serialize.ExternalAccountResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	externalAccount, err := s.externalAccountRepo.QueryByIDAndUserID(ctx, s.db, params.ID, params.User.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if externalAccount == nil {
		return nil, apierror.ExternalAccountNotFound()
	}

	if !oauth.ProviderExists(externalAccount.Provider) {
		return nil, apierror.UnsupportedOauthProvider(externalAccount.Provider)
	}

	if !userSettings.EnabledSocialStrategies().Contains(externalAccount.Provider) {
		return nil, apierror.OAuthProviderNotEnabled(externalAccount.Provider)
	}

	identification, err := s.identificationRepo.FindByIDAndInstance(ctx, s.db, externalAccount.IdentificationID, externalAccount.InstanceID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	prepareForm := sharedstrategies.OAuthPrepareForm{
		Strategy:                  externalAccount.Provider,
		RedirectURL:               params.RedirectURL,
		ActionCompleteRedirectURL: params.ActionCompleteRedirectURL,
		Origin:                    params.Origin,
		SourceType:                constants.OSTOAuthReauthorize,
		SourceID:                  externalAccount.ID,
		ClientID:                  client.ID,
		AdditionalScopes:          params.AdditionalScopes,
	}
	if externalAccount.EmailAddress != "" {
		prepareForm.LoginHint = &externalAccount.EmailAddress
	}

	var verificationWithStatus *model.VerificationWithStatus
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		preparer := sharedstrategies.NewOAuthPreparer(s.clock, env, prepareForm)
		verification, err := preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		verificationWithStatus = &model.VerificationWithStatus{Verification: verification, Status: constants.VERUnverified}

		verification.IdentificationID = null.StringFrom(identification.ID)
		if err = s.verificationRepo.UpdateIdentificationID(ctx, tx, verification); err != nil {
			return true, err
		}

		identification.VerificationID = null.StringFrom(verification.ID)
		identification.Status = constants.ISNotSet
		if err = s.identificationRepo.UpdateVerificationIDAndStatus(ctx, tx, identification); err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		if errors.Is(txErr, sharedstrategies.ErrFailedExchangeCredentialsOAuth1) {
			return nil, apierror.MisconfiguredOAuthProvider()
		} else if errors.Is(txErr, sharedstrategies.ErrSharedCredentialsNotAvailable) {
			return nil, apierror.OAuthSharedCredentialsNotSupported()
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.ExternalAccount(ctx, externalAccount, verificationWithStatus), nil
}

// DeleteExternalAccount deletes the external account after ensuring that the user won't be locked out due to the deletion.
func (s *Service) DeleteExternalAccount(ctx context.Context, user *model.User, externalAccountID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	externalAccount, err := s.externalAccountRepo.QueryByIDAndUserID(ctx, s.db, externalAccountID, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if externalAccount == nil {
		return nil, apierror.ExternalAccountNotFound()
	}

	return s.DeleteIdentification(ctx, user, externalAccount.IdentificationID)
}

func (s *Service) PrepareVerification(
	ctx context.Context,
	user *model.User,
	prepareForm strategies.VerificationPrepareForm) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.VerificationStrategies().Contains(prepareForm.Strategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, prepareForm.Strategy)
	}

	selectedStrategy, strategyExists := strategies.GetStrategy(prepareForm.Strategy)
	if !strategyExists || !strategies.IsPreparableDuringVerification(selectedStrategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, prepareForm.Strategy)
	}
	strategy := selectedStrategy.(strategies.VerificationPreparable)

	var identification *model.Identification
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		preparer, apiErr := strategy.CreateVerificationPreparer(ctx, tx, s.deps, env, prepareForm)
		if apiErr != nil {
			return true, apiErr
		}

		identification = preparer.Identification()

		if identification.UserID.String != user.ID {
			return true, apierror.IdentificationBelongsToDifferentUser()
		}

		verification, err := preparer.Prepare(ctx, tx)
		if err != nil {
			return true, err
		}

		identification.VerificationID = null.StringFrom(verification.ID)
		if err := s.identificationRepo.UpdateVerificationID(ctx, tx, identification); err != nil {
			return true, err
		}

		if err = s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, user); err != nil {
			return true, fmt.Errorf("user/PrepareVerification: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err)
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		}

		return nil, apierror.Unexpected(txErr)
	}

	return s.toIdentificationResponse(ctx, identification)
}

// AttemptVerification will attempt to verify a Verification.
// The Identification is updated accordingly and the identification's User is touched.
func (s *Service) AttemptVerification(
	ctx context.Context,
	user *model.User,
	attemptForm strategies.VerificationAttemptForm) (interface{}, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	if !userSettings.VerificationStrategies().Contains(attemptForm.Strategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, attemptForm.Strategy)
	}

	selectedStrategy, strategyExists := strategies.GetStrategy(attemptForm.Strategy)
	if !strategyExists || !strategies.IsAttemptableDuringVerification(selectedStrategy) {
		return nil, apierror.FormInvalidParameterValue(param.Strategy.Name, attemptForm.Strategy)
	}
	strategy := selectedStrategy.(strategies.VerificationAttemptable)

	var identification *model.Identification
	var attemptor sharedstrategies.Attemptor
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var apiErr apierror.Error
		attemptor, apiErr = strategy.CreateVerificationAttemptor(ctx, tx, s.deps, env, attemptForm)
		if apiErr != nil {
			return true, apiErr
		}

		verification, err := sharedstrategies.AttemptVerification(ctx, tx, attemptor, s.verificationRepo, client.ID)
		if errors.Is(err, sharedstrategies.ErrInvalidCode) {
			return false, err
		} else if errors.Is(err, sharedstrategies.ErrInvalidPassword) {
			return false, err
		} else if err != nil {
			return true, err
		}

		identification, err = s.identificationRepo.FindByVerification(ctx, tx, verification.ID)
		if err != nil {
			return true, err
		}

		if identification.UserID.String != user.ID {
			return true, apierror.IdentificationBelongsToDifferentUser()
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		} else if attemptor != nil {
			return nil, attemptor.ToAPIError(txErr)
		}
		return nil, apierror.Unexpected(txErr)
	}

	// After successful verification actions
	txErr = s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.identificationService.FinalizeVerification(ctx, tx, identification, env.Instance, userSettings)
		if err != nil {
			return true, err
		}

		if identification.IsEmailAddress() {
			err := s.orgDomainService.CreateInvitationsSuggestionsForUserEmail(ctx, tx, env.AuthConfig, *identification.EmailAddress(), env.Instance.ID, user.ID)
			if err != nil {
				return true, err
			}
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		if errors.Is(txErr, identifications.ErrIdentifierAlreadyExists) {
			return nil, apierror.IdentificationExists(identification.Type, nil)
		}
		return nil, apierror.Unexpected(txErr)
	}

	return s.toIdentificationResponse(ctx, identification)
}

func (s *Service) CreateTOTP(ctx context.Context, user *model.User) (*serialize.TOTPResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.GetAttribute(names.AuthenticatorApp).Base().Enabled {
		return nil, apierror.FeatureNotEnabled()
	}

	existingTOTP, err := s.totpRepo.QueryVerifiedByUser(ctx, s.db, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if existingTOTP != nil {
		return nil, apierror.TOTPAlreadyEnabled()
	}

	accountName := s.userProfileService.GetIdentifier(ctx, s.db, user)

	issuer := env.Application.Name
	if !env.Instance.IsProduction() {
		issuer = fmt.Sprintf("%s (%s)", issuer, env.Instance.EnvironmentType)
	}

	secret, qrURI, err := totp.Generate(issuer, accountName)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	newTOTP := &model.TOTP{Totp: &sqbmodel.Totp{
		InstanceID: env.Instance.ID,
		UserID:     user.ID,
		Secret:     secret,
		Verified:   false,
	}}
	if err = s.totpRepo.Upsert(ctx, s.db, newTOTP); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.TOTP(newTOTP, qrURI), nil
}

func (s *Service) AttemptTOTPVerification(ctx context.Context, user *model.User, code string) (*serialize.TOTPResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.GetAttribute(names.AuthenticatorApp).Base().Enabled {
		return nil, apierror.FeatureNotEnabled()
	}

	existingTOTP, err := s.totpRepo.QueryByUser(ctx, s.db, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if existingTOTP == nil {
		return nil, apierror.ResourceNotFound()
	}

	if existingTOTP.Verified {
		return nil, apierror.TOTPAlreadyEnabled()
	}

	var attemptor sharedstrategies.Attemptor
	var backupCodes []string
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		verification := &model.Verification{Verification: &sqbmodel.Verification{
			InstanceID: env.Instance.ID,
			Strategy:   constants.VSTOTP,
			Attempts:   0,
		}}

		if err = s.verificationRepo.Insert(ctx, tx, verification); err != nil {
			return true, err
		}

		attemptor = sharedstrategies.NewTOTPAttemptor(s.clock, verification, existingTOTP.Secret, code, env.Instance.ID)
		_, err = attemptor.Attempt(ctx, tx)
		if err != nil {
			return true, err
		}

		existingTOTP.Verified = true
		if err = s.totpRepo.UpdateVerified(ctx, tx, existingTOTP); err != nil {
			return true, err
		}

		backupCodes, err = s.finalizeMFAEnablement(ctx, tx, userSettings, user.ID, env.Instance.ID)
		if err != nil {
			return true, err
		}

		// Also touch user because this affects the user.totp_enabled calculated property
		if err = s.userRepo.UpdateUpdatedAtByID(ctx, tx, user.ID); err != nil {
			return true, err
		}

		if err = s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, user); err != nil {
			return true, fmt.Errorf("user/AttemptTOTPVerification: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err)
		}

		return false, nil
	})
	if txErr != nil {
		return nil, attemptor.ToAPIError(txErr)
	}

	return serialize.TOTPAttempt(existingTOTP, backupCodes), nil
}

func (s *Service) DeleteTOTP(ctx context.Context, user *model.User) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	existingTOTP, err := s.totpRepo.QueryByUser(ctx, s.db, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if existingTOTP == nil {
		return nil, apierror.ResourceNotFound()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		if err = s.totpRepo.DeleteByUser(ctx, tx, user.ID); err != nil {
			return true, err
		}

		// Also touch user because this affects the user.totp_enabled calculated property
		if err = s.userRepo.UpdateUpdatedAtByID(ctx, tx, user.ID); err != nil {
			return true, err
		}

		if err = s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, user); err != nil {
			return true, fmt.Errorf("user/DeleteTOTP: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err)
		}

		return s.cleanupBackupCodes(ctx, tx, userSettings, user)
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.DeletedObject(existingTOTP.ID, serialize.TOTPObjectName), nil
}

func (s *Service) CreateBackupCodes(ctx context.Context, user *model.User) (*serialize.BackupCodeResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.GetAttribute(names.BackupCode).Base().Enabled {
		return nil, apierror.FeatureNotEnabled()
	}

	// In order for a user to generate backup codes, they must have another MFA enabled
	userHasMFAEnabled, err := s.userProfileService.HasTwoFactorEnabled(ctx, s.db, userSettings, user.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if !userHasMFAEnabled {
		return nil, apierror.BackupCodesNotAvailable()
	}

	var response *serialize.BackupCodeResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		newBackupCode, plainCodes, err := s.createBackupCodes(ctx, tx, user.ID, env.Instance.ID)
		if err != nil {
			return true, err
		}

		// Also touch user because this affects the user.backup_code_enabled calculated property
		if err = s.userRepo.UpdateUpdatedAtByID(ctx, tx, user.ID); err != nil {
			return true, err
		}

		if err = s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, user); err != nil {
			return true, fmt.Errorf("user/CreateBackupCodes: send user updated event for (%+v, %+v): %w", user, env.Instance.ID, err)
		}

		response = serialize.BackupCode(newBackupCode, plainCodes)

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	return response, nil
}

func (s *Service) toIdentificationResponse(ctx context.Context, ident *model.Identification) (interface{}, apierror.Error) {
	identificationSerializable, err := s.serializableService.ConvertIdentification(ctx, s.db, ident)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response, err := serialize.Identification(identificationSerializable)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return response, nil
}

func (s *Service) fetchIdentification(ctx context.Context, identifierID, instanceID, userID string) (*model.Identification, apierror.Error) {
	identification, err := s.identificationRepo.QueryByIDAndUser(ctx, s.db, instanceID, identifierID, userID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if identification == nil {
		return nil, apierror.IdentificationNotFound(identifierID)
	}

	return identification, nil
}

func (s *Service) createIdentification(
	ctx context.Context,
	createIdentificationData identifications.CreateIdentificationData,
	user *model.User,
	instance *model.Instance,
	authConfig *model.AuthConfig,
) (*model.Identification, apierror.Error) {
	userSettings := usersettings.NewUserSettings(authConfig.UserSettings)
	attribute := userSettings.GetAttribute(names.AttributeName(createIdentificationData.Type))

	var createdIdentification *model.Identification
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var exists bool
		var err error
		if attribute.Base().VerifyAtSignUp {
			exists, err = s.identificationRepo.ExistsVerifiedByIdentifierAndType(ctx, tx, createIdentificationData.Identifier, createIdentificationData.Type, instance.ID)
		} else {
			exists, err = s.identificationRepo.ExistsVerifiedOrReservedByIdentifierAndType(ctx, tx, createIdentificationData.Identifier, createIdentificationData.Type, instance.ID)
		}

		if err != nil {
			return true, apierror.Unexpected(err)
		}
		if exists {
			return true, apierror.FormIdentifierExists(createIdentificationData.Type)
		}

		identification, err := s.identificationService.CreateIdentification(ctx, tx, createIdentificationData)
		if err != nil {
			if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueIdentification) {
				return true, apierror.FormIdentifierExists(createIdentificationData.Type)
			}
			return true, err
		}

		if err = s.sendUserUpdatedEvent(ctx, tx, instance, userSettings, user); err != nil {
			return true, fmt.Errorf("user/update: send user updated event for (%+v, %+v): %w", user, instance.ID, err)
		}

		createdIdentification = identification
		return false, nil
	})

	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}
	return createdIdentification, nil
}

type ListOrganizationMembershipsParams struct {
	UserID    string
	Paginated *bool
}

func (s *Service) ListOrganizationMemberships(
	ctx context.Context,
	params ListOrganizationMembershipsParams,
	paginationParams pagination.Params,
) (interface{}, apierror.Error) {
	memberships, apiErr := s.organizationService.ListMemberships(ctx, s.db, organizations.ListMembershipsParams{
		UserID: &params.UserID,
	}, paginationParams)
	if apiErr != nil {
		return nil, apiErr
	}

	// Serialize results
	res := make([]interface{}, len(memberships))
	for i, membership := range memberships {
		res[i] = serialize.OrganizationMembership(ctx, membership)
	}

	if params.Paginated != nil && *params.Paginated {
		count, err := s.organizationMemberRepo.CountByUser(ctx, s.db, params.UserID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}

		return serialize.Paginated(res, count), nil
	}

	return res, nil
}

func (s *Service) DeleteOrganizationMembership(ctx context.Context, organizationID, userID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	var membershipSerializable *model.OrganizationMembershipSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var err error
		membershipSerializable, err = s.organizationService.DeleteMembership(ctx, organizations.DeleteMembershipParams{
			OrganizationID:   organizationID,
			UserID:           userID,
			RequestingUserID: userID,
			Env:              env,
		})
		return err != nil, err
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.DeletedObject(membershipSerializable.OrganizationMembership.ID, serialize.ObjectOrganizationMembership), nil
}

type ListOrganizationInvitationsParams struct {
	UserID   string
	Statuses []string
}

func (p ListOrganizationInvitationsParams) validate() apierror.Error {
	for _, status := range p.Statuses {
		if !constants.OrganizationInvitationStatuses.Contains(status) {
			return apierror.FormInvalidParameterValueWithAllowed(param.Status.Name, status, constants.OrganizationInvitationStatuses.Array())
		}
	}

	return nil
}

func (s *Service) ListOrganizationInvitations(
	ctx context.Context,
	params ListOrganizationInvitationsParams,
	paginationParams pagination.Params,
) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(); apiErr != nil {
		return nil, apiErr
	}

	invitations, err := s.organizationService.ListInvitationsForUser(ctx, s.db, env.Instance.ID, params.UserID, params.Statuses, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalInvitations, err := s.organizationInvitationRepo.CountByInstanceAndUserAndStatus(ctx, s.db, env.Instance.ID, params.UserID, params.Statuses)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]interface{}, len(invitations))
	for i, invitation := range invitations {
		response[i] = serialize.OrganizationInvitationMe(ctx, invitation.Serializable, invitation.Organization)
	}

	return serialize.Paginated(response, totalInvitations), nil
}

func (s *Service) AcceptOrganizationInvitation(ctx context.Context, invitationID, userID string) (*serialize.OrganizationInvitationResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	invitation, err := s.organizationInvitationRepo.QueryByIDAndUser(ctx, s.db, invitationID, userID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if invitation == nil {
		return nil, apierror.OrganizationInvitationNotFound(invitationID)
	}

	if !invitation.IsPending() {
		return nil, apierror.OrganizationInvitationNotPending()
	}

	organization, err := s.organizationRepo.FindByID(ctx, s.db, invitation.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var acceptedInvitation *model.OrganizationInvitationSerializable
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		acceptedInvitation, err = s.organizationService.AcceptInvitation(ctx, tx, organizations.AcceptInvitationParams{
			InvitationID: invitationID,
			UserID:       userID,
			Instance:     env.Instance,
			Subscription: env.Subscription,
		})
		if err != nil {
			return true, err
		}

		emailIdents, err := s.getEmailsByPermissionKey(ctx, tx, constants.PermissionMembersManage, organization.ID, env.Instance.ID)
		if err != nil {
			return true, err
		}

		// send organization invitation accepted email to all org admins
		emailParams := comms.EmailOrganizationInvitationAccepted{
			Organization:  organization,
			EmailAddress:  invitation.EmailAddress,
			ToEmailIdents: emailIdents,
		}
		if err = s.commsService.SendOrganizationInvitationAcceptedEmails(ctx, tx, env, emailParams); err != nil {
			return true, fmt.Errorf("sending organization invitation accepted emails failed: %w", err)
		}

		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIErr := apierror.As(txErr); isAPIErr {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationInvitationMe(ctx, acceptedInvitation, organization), nil
}

type ListOrganizationSuggestionsParams struct {
	UserID   string
	Statuses []string
}

func (p ListOrganizationSuggestionsParams) validate() apierror.Error {
	for _, status := range p.Statuses {
		if !constants.OrganizationSuggestionStatuses.Contains(status) {
			return apierror.FormInvalidParameterValueWithAllowed(param.Status.Name, status, constants.OrganizationSuggestionStatuses.Array())
		}
	}

	return nil
}

func (s *Service) ListOrganizationSuggestions(
	ctx context.Context,
	params ListOrganizationSuggestionsParams,
	paginationParams pagination.Params,
) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(); apiErr != nil {
		return nil, apiErr
	}

	suggestions, err := s.organizationSuggestionRepo.FindAllByInstanceAndUserAndStatus(ctx, s.db, env.Instance.ID, params.UserID, params.Statuses, paginationParams)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	totalSuggestions, err := s.organizationSuggestionRepo.CountByInstanceAndUserAndStatus(ctx, s.db, env.Instance.ID, params.UserID, params.Statuses)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	response := make([]interface{}, len(suggestions))
	for i, suggestion := range suggestions {
		response[i] = serialize.OrganizationSuggestionMe(ctx, &suggestion.OrganizationSuggestion, &suggestion.Organization)
	}

	return serialize.Paginated(response, totalSuggestions), nil
}

func (s *Service) AcceptOrganizationSuggestion(ctx context.Context, suggestionID, userID string) (*serialize.OrganizationSuggestionResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	suggestion, err := s.organizationSuggestionRepo.QueryPendingByInstanceAndIDAndUser(ctx, s.db, env.Instance.ID, suggestionID, userID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if suggestion == nil {
		return nil, apierror.ResourceNotFound()
	}

	organization, err := s.organizationRepo.QueryByID(ctx, s.db, suggestion.OrganizationID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if organization == nil {
		return nil, apierror.ResourceNotFound()
	}

	// Check if user is already a member of the organization
	alreadyMember, err := s.organizationMemberRepo.ExistsByOrganizationAndUser(ctx, s.db, organization.ID, userID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if alreadyMember {
		return nil, apierror.AlreadyAMemberOfOrganization(userID)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		membershipRequest := &model.OrganizationMembershipRequest{
			OrganizationMembershipRequest: &sqbmodel.OrganizationMembershipRequest{
				InstanceID:               suggestion.InstanceID,
				OrganizationID:           suggestion.OrganizationID,
				UserID:                   userID,
				OrganizationDomainID:     suggestion.OrganizationDomainID,
				OrganizationSuggestionID: suggestion.ID,
				Status:                   constants.StatusPending,
			},
		}
		if err := s.orgMemberRequestRepo.Insert(ctx, tx, membershipRequest); err != nil {
			return true, err
		}

		suggestion.Status = constants.StatusAccepted
		if err := s.organizationSuggestionRepo.UpdateStatus(ctx, tx, suggestion); err != nil {
			return true, err
		}

		emailIdents, err := s.getEmailsByPermissionKey(ctx, tx, constants.PermissionDomainsManage, organization.ID, env.Instance.ID)
		if err != nil {
			return true, err
		}

		// send organization membership requested email to all org admins
		emailParams := comms.EmailOrganizationMembershipRequested{
			Organization:  organization,
			EmailAddress:  suggestion.EmailAddress,
			ToEmailIdents: emailIdents,
		}
		if err = s.commsService.SendOrganizationMembershipRequestedEmails(ctx, tx, env, emailParams); err != nil {
			return true, fmt.Errorf("sending organization membership requested emails failed: %w", err)
		}

		return false, nil
	})
	if txErr != nil {
		if clerkerrors.IsUniqueConstraintViolation(txErr, clerkerrors.UniqueOrganizationMembershipRequestOrgUser) {
			return nil, apierror.OrganizationSuggestionAlreadyAccepted()
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialize.OrganizationSuggestionMe(ctx, suggestion, organization), nil
}

func (s *Service) getEmailsByPermissionKey(ctx context.Context, tx database.Tx, permKey, organizationID, instanceID string) ([]*model.Identification, error) {
	permission, err := s.permissionRepo.FindSystemByKeyAndInstance(ctx, tx, permKey, instanceID)
	if err != nil {
		return nil, err
	}
	emailIdents, err := s.identificationRepo.FindAllEmailsByOrganizationAndPermission(ctx, tx, organizationID, permission.ID)
	if err != nil {
		return nil, err
	}
	return emailIdents, nil
}

func (s *Service) cleanupBackupCodes(ctx context.Context, tx database.Executor, userSettings *usersettings.UserSettings, user *model.User) (bool, error) {
	enabled, err := s.userProfileService.HasTwoFactorEnabled(ctx, tx, userSettings, user.ID)
	if err != nil {
		return true, err
	}

	if !enabled {
		err = s.backupCodeRepo.DeleteByUser(ctx, tx, user.ID)
		if err != nil {
			return true, err
		}
	}

	return false, nil
}

func (s *Service) createBackupCodes(ctx context.Context, tx database.Executor, userID, instanceID string) (*model.BackupCode, []string, error) {
	plainCodes, hashedCodes, err := backup_codes.GenerateAndHash()
	if err != nil {
		return nil, nil, err
	}

	newBackupCode := &model.BackupCode{BackupCode: &sqbmodel.BackupCode{
		InstanceID: instanceID,
		UserID:     userID,
		Codes:      hashedCodes,
	}}
	if err = s.backupCodeRepo.Upsert(ctx, tx, newBackupCode); err != nil {
		return nil, nil, err
	}

	return newBackupCode, plainCodes, nil
}

func (s *Service) finalizeMFAEnablement(ctx context.Context, tx database.Executor, userSettings *usersettings.UserSettings, userID, instanceID string) ([]string, error) {
	if !userSettings.GetAttribute(names.BackupCode).Base().Enabled {
		return nil, nil
	}

	backupCodesAlreadyExists, err := s.backupCodeRepo.ExistsByUser(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if backupCodesAlreadyExists {
		return nil, nil
	}

	_, plainCodes, err := s.createBackupCodes(ctx, tx, userID, instanceID)
	if err != nil {
		return nil, err
	}

	return plainCodes, nil
}

type ChangePasswordParams struct {
	CurrentPassword        *string
	NewPassword            string
	SignOutOfOtherSessions *bool
	User                   *model.User
}

func (params ChangePasswordParams) validate(ctx context.Context, passwordSettings usersettingsmodel.PasswordSettings) apierror.Error {
	if params.User.PasswordDigest.Valid {
		if params.CurrentPassword == nil {
			// if user has a password, the `current_password` param needs to be included
			// in the incoming request
			return apierror.FormMissingParameter(param.CurrentPassword.Name)
		}

		err := matchUserPassword(params.User, *params.CurrentPassword, param.CurrentPassword.Name)
		if err != nil {
			return err
		}
	} else if params.CurrentPassword != nil {
		// if user doesn't have a password, `current_password` param should not be
		// included
		return apierror.FormUnknownParameter(param.CurrentPassword.Name)
	}

	return validate.Password(ctx, params.NewPassword, param.NewPassword.Name, passwordSettings)
}

func (s *Service) ChangePassword(ctx context.Context, params ChangePasswordParams) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	requestingSession := requesting_session.FromContext(ctx)

	apiErr := params.validate(ctx, env.AuthConfig.UserSettings.PasswordSettings)
	if apiErr != nil {
		return nil, apiErr
	}

	passwordDigest, err := hash.GenerateBcryptHash(params.NewPassword)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var response *serialize.UserResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		signOutOfOtherSessions := false
		if params.SignOutOfOtherSessions != nil {
			signOutOfOtherSessions = *params.SignOutOfOtherSessions
		}

		err := s.passwordService.ChangeUserPassword(ctx, tx, password.ChangeUserPasswordParams{
			Env:                    env,
			PasswordDigest:         passwordDigest,
			PasswordHasher:         hash.Bcrypt,
			RequestingSessionID:    &requestingSession.ID,
			SignOutOfOtherSessions: signOutOfOtherSessions,
			User:                   params.User,
		})
		if err != nil {
			return true, err
		}

		userSerializable, err := s.serializableService.ConvertUser(ctx, tx, userSettings, params.User)
		if err != nil {
			return true, err
		}

		response = serialize.UserToClientAPI(ctx, userSerializable)

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}
	return response, nil
}

type DeletePasswordParams struct {
	CurrentPassword string
	User            *model.User
}

func (s *Service) DeletePassword(ctx context.Context, params DeletePasswordParams) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	// Check that passwords are not required for the instance
	if userSettings.IsRequired(names.Password) {
		return nil, apierror.PasswordRequired()
	}

	// Check that user has a password to delete
	if !params.User.PasswordDigest.Valid {
		return nil, apierror.NoPasswordSet()
	}

	err := matchUserPassword(params.User, params.CurrentPassword, param.CurrentPassword.Name)
	if err != nil {
		return nil, err
	}

	var response *serialize.UserResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		params.User.PasswordDigest = null.StringFromPtr(nil)
		params.User.PasswordHasher = null.StringFromPtr(nil)
		err := s.userRepo.UpdatePasswordDigestAndHasher(ctx, tx, params.User)
		if err != nil {
			return true, err
		}

		userSerializable, err := s.serializableService.ConvertUser(ctx, tx, userSettings, params.User)
		if err != nil {
			return true, err
		}

		response = serialize.UserToClientAPI(ctx, userSerializable)

		err = s.sendPasswordRemovedNotification(ctx, tx, env, params.User)
		if err != nil {
			return true, err
		}

		err = s.sendUserUpdatedEvent(ctx, tx, env.Instance, userSettings, params.User)
		if err != nil {
			return true, fmt.Errorf("send user updated event for (%s, %s): %w", params.User.ID, env.Instance.ID, err)
		}

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}
	return response, nil
}

func (s *Service) sendPasswordRemovedNotification(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	user *model.User) error {
	primaryEmailAddress, err := s.userProfileService.GetPrimaryEmailAddress(ctx, tx, user)
	if err != nil {
		return err
	}
	if primaryEmailAddress != nil {
		return s.commsService.SendPasswordRemovedEmail(ctx, tx, env, comms.EmailPasswordRemoved{
			GreetingName: strings.TrimSpace(fmt.Sprintf("%s %s",
				user.FirstName.String, user.LastName.String)),
			PrimaryEmailAddress: *primaryEmailAddress,
		})
	}

	primaryPhoneNumber, err := s.userProfileService.GetPrimaryPhoneNumber(ctx, tx, user)
	if err != nil {
		return err
	}
	if primaryPhoneNumber != nil {
		return s.commsService.SendPasswordChangedSMS(ctx, tx, env, *primaryPhoneNumber)
	}
	return nil
}

func matchUserPassword(user *model.User, password string, paramName string) apierror.Error {
	isValid, err := hash.Compare(user.PasswordHasher.String, password, user.PasswordDigest.String)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if !isValid {
		return apierror.FormPasswordValidationFailed(paramName)
	}
	return nil
}

func (s *Service) Update(ctx context.Context, params users.UpdateForm) (*serialize.UserResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	user := requesting_user.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	// TODO bsid:
	// Remove this feature once we add "sudo auth" AND we've talked with finary.com.
	if params.PrimaryEmailAddressID != nil && env.AuthConfig.ExperimentalSettings.DisableUpdatePrimaryEmailAddress {
		return nil, apierror.FormUnknownParameter(param.PrimaryEmailAddressID.Name)
	}

	if params.Password != nil &&
		!env.AuthConfig.ExperimentalSettings.PatchMePasswordEnabled() {
		return nil, apierror.UpdatingUserPasswordDeprecated()
	}

	var serialized *serialize.UserResponse
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		// if primary email address is changed, notify the user.
		params.PrimaryEmailAddressNotify = params.PrimaryEmailAddressID != nil

		updatedUser, apiErr := s.userService.Update(ctx, env, user.ID, &params, env.Instance, userSettings)
		if apiErr != nil {
			return true, apiErr
		}

		userSerializable, err := s.serializableService.ConvertUser(ctx, tx, userSettings, updatedUser)
		if err != nil {
			return true, err
		}

		serialized = serialize.UserToClientAPI(ctx, userSerializable)

		return false, nil
	})
	if txErr != nil {
		if apiErr, ok := apierror.As(txErr); ok {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	return serialized, nil
}

func (s *Service) Delete(ctx context.Context, user *model.User) (*serialize.DeletedObjectResponse, apierror.Error) {
	if !user.DeleteSelfEnabled {
		return nil, apierror.UserDeleteSelfNotEnabled()
	}

	env := environment.FromContext(ctx)
	return s.userService.Delete(ctx, env, user.ID)
}

func (s *Service) sendUserUpdatedEvent(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User) error {
	userSerializable, err := s.serializableService.ConvertUser(ctx, exec, userSettings, user)
	if err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: serializing user %+v: %w", user, err)
	}

	if err = s.eventService.UserUpdated(ctx, exec, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return fmt.Errorf("sendUserUpdatedEvent: send user updated event for user %s: %w", user.ID, err)
	}
	return nil
}

// resolveExternalAccountConflict handles conflicts with existing accounts by
// unlinking the account for re-verification when needed, or by returning an error
// in all other cases.
func (s *Service) resolveExternalAccountConflict(
	ctx context.Context,
	providerID string,
	extAccount *model.ExternalAccount,
) (isReVerificationNeeded bool, apiErr apierror.Error) {
	if extAccount == nil {
		return false, nil
	}

	ident, err := s.identificationRepo.FindByID(ctx, s.db, extAccount.IdentificationID)
	if err != nil {
		return false, apierror.Unexpected(err)
	}

	if ident.VerificationID.Valid {
		ver, vErr := s.verificationRepo.FindByID(ctx, s.db, ident.VerificationID.String)
		if vErr != nil {
			return false, apierror.Unexpected(err)
		}
		isReVerificationNeeded = ver.HasErrorCode(apierror.ExternalAccountMissingRefreshTokenCode)
	}

	isReAuthorizeAllowed := ident.RequiresVerification.Bool || isReVerificationNeeded
	if !isReAuthorizeAllowed {
		return false, apierror.OAuthAccountAlreadyConnected(providerID)
	}

	ident.UserID = null.StringFromPtr(nil)
	if err = s.identificationRepo.UpdateUserID(ctx, s.db, ident); err != nil {
		return false, apierror.Unexpected(err)
	}

	return true, nil
}
