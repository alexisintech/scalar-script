package sign_up

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"clerk/api/apierror"
	"clerk/api/shared/client_data"
	"clerk/api/shared/cookies"
	"clerk/api/shared/events"
	"clerk/api/shared/externalaccount"
	"clerk/api/shared/gamp"
	"clerk/api/shared/identifications"
	"clerk/api/shared/images"
	"clerk/api/shared/organizations"
	"clerk/api/shared/orgdomain"
	"clerk/api/shared/restrictions"
	"clerk/api/shared/serializable"
	"clerk/api/shared/sessions"
	"clerk/api/shared/users"
	"clerk/api/shared/validators"
	"clerk/api/shared/verifications"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctxkeys"
	"clerk/pkg/emailaddress"
	"clerk/pkg/jobs"
	clerkjson "clerk/pkg/json"
	"clerk/pkg/oauth"
	"clerk/pkg/rand"
	"clerk/pkg/set"
	"clerk/pkg/strings"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/pkg/usersettings/clerk/names"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
	"github.com/volatiletech/sqlboiler/v4/types"
)

type Service struct {
	clock     clockwork.Clock
	gueClient *gue.Client
	db        database.Database

	// services
	cookieService          *cookies.Service
	eventService           *events.Service
	gampService            *gamp.Service
	externalAccountService *externalaccount.Service
	identificationService  *identifications.Service
	orgDomainService       *orgdomain.Service
	organizationService    *organizations.Service
	restrictionService     *restrictions.Service
	serializableService    *serializable.Service
	sessionService         *sessions.Service
	userService            *users.CreateService
	validatorService       *validators.Service
	verificationService    *verifications.Service
	imageService           *images.Service
	clientDataService      *client_data.Service

	// repositories
	dailySuccessfulSignUps *repository.DailySuccessfulSignUps
	identificationRepo     *repository.Identification
	invitationRepo         *repository.Invitations
	orgInvitationRepo      *repository.OrganizationInvitation
	signUpRepo             *repository.SignUp
	userRepo               *repository.Users
	verificationRepo       *repository.Verification
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                  deps.Clock(),
		gueClient:              deps.GueClient(),
		db:                     deps.DB(),
		cookieService:          cookies.NewService(deps),
		eventService:           events.NewService(deps),
		gampService:            gamp.NewService(deps),
		externalAccountService: externalaccount.NewService(deps),
		identificationService:  identifications.NewService(deps),
		orgDomainService:       orgdomain.NewService(deps.Clock()),
		organizationService:    organizations.NewService(deps),
		imageService:           images.NewService(deps.StorageClient()),
		restrictionService:     restrictions.NewService(deps.EmailQualityChecker()),
		serializableService:    serializable.NewService(deps.Clock()),
		sessionService:         sessions.NewService(deps),
		userService:            users.NewCreateService(deps.Clock()),
		clientDataService:      client_data.NewService(deps),
		validatorService:       validators.NewService(),
		verificationService:    verifications.NewService(deps.Clock()),
		dailySuccessfulSignUps: repository.NewDailySuccessfulSignUps(),
		identificationRepo:     repository.NewIdentification(),
		invitationRepo:         repository.NewInvitations(),
		orgInvitationRepo:      repository.NewOrganizationInvitation(),
		signUpRepo:             repository.NewSignUp(),
		userRepo:               repository.NewUsers(),
		verificationRepo:       repository.NewVerification(),
	}
}

func (s *Service) convertToUser(
	ctx context.Context,
	tx database.Tx,
	client *model.Client,
	env *model.Env,
	signUp *model.SignUp,
	externalAccount *model.ExternalAccount,
	postponeCookieUpdate bool,
	rotatingTokenNonce *string,
) (*model.Session, error) {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	// Important note: the session records are retrieved and updated outside of the database transaction here.
	cdsSessions, err := s.clientDataService.FindAllCurrentSessionsByClients(ctx, env.Instance.ID, []string{client.ID})
	if err != nil {
		return nil, err
	}
	currentSessions := client_data.ToSessionModels(cdsSessions)

	// if we're in single_session_mode and there's any session on the client, mark it removed
	if env.AuthConfig.SessionSettings.SingleSessionMode {
		err := s.sessionService.RemoveAllWithoutClientTouch(ctx, currentSessions)
		if err != nil {
			return nil, err
		}

		// in single_session_mode, remove sign_in on successful sign_up
		if client.SignInID.Valid {
			client.SignInID = null.StringFromPtr(nil)
			cdsClient := client_data.NewClientFromClientModel(client)
			if err := s.clientDataService.UpdateClientSignInID(ctx, env.Instance.ID, cdsClient); err != nil {
				return nil, err
			}
			cdsClient.CopyToClientModel(client)
		}
	} else {
		// check if we already have an active impersonation session in the client,
		// if we do, we shouldn't allow the creation of new sessions
		for _, session := range currentSessions {
			if session.IsActive(s.clock) && session.HasActor() {
				return nil, apierror.CannotCreateSessionWhenImpersonationIsPresent()
			}
		}
	}

	user := &model.User{User: &sqbmodel.User{
		InstanceID:     env.Instance.ID,
		PasswordDigest: signUp.PasswordDigest,
		PasswordHasher: signUp.PasswordHasher,
		FirstName:      signUp.FirstName,
		LastName:       signUp.LastName,
		ExternalID:     signUp.ExternalID,
		LastActiveAt:   null.TimeFrom(s.clock.Now().UTC()),
	}}

	if signUp.UnsafeMetadata != nil {
		user.UnsafeMetadata = signUp.UnsafeMetadata
	}

	if signUp.PublicMetadata != nil {
		user.PublicMetadata = signUp.PublicMetadata
	}

	if externalAccount != nil && externalAccount.AvatarURL != "" {
		user.ProfileImagePublicURL = null.StringFrom(externalAccount.AvatarURL)
	}

	err = s.userService.Create(ctx, tx, users.CreateParams{
		AuthConfig:   env.AuthConfig,
		Instance:     env.Instance,
		Subscription: env.Subscription,
		User:         user,
	})
	if err != nil {
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueExternalID) {
			return nil, apierror.FormIdentifierExists(param.ExternalID.Name)
		}

		return nil, err
	}

	// add user in organization if an organization invitation was used
	var activeOrganizationID *string
	if signUp.OrganizationInvitationID.Valid {
		invitation, err := s.organizationService.AcceptInvitation(ctx, tx, organizations.AcceptInvitationParams{
			InvitationID: signUp.OrganizationInvitationID.String,
			UserID:       user.ID,
			Instance:     env.Instance,
			Subscription: env.Subscription,
		})
		if err != nil {
			return nil, err
		}

		activeOrganizationID = &invitation.OrganizationID
	}

	// If needed, initiate the unverified email verification flow, which currently applies only to instances
	// that allow unverified emails at sign-up
	if err = s.identificationService.FinalizeReVerifyFlow(ctx, tx, env.Instance.ID, user.ID); err != nil {
		return nil, err
	}

	if externalAccount != nil {
		if err := s.externalAccountService.EnsureRefreshTokenExists(ctx, tx, externalAccount); err != nil {
			return nil, err
		}
	}

	// create session
	userSession, err := s.sessionService.Create(ctx, tx, sessions.CreateParams{
		AuthConfig:           env.AuthConfig,
		Instance:             env.Instance,
		ClientID:             signUp.ClientID,
		User:                 user,
		ActivityID:           signUp.SessionActivityID.Ptr(),
		ExternalAccount:      externalAccount,
		ActiveOrganizationID: activeOrganizationID,
		SessionStatus:        strings.ToPtr(constants.SESSPendingActivation),
	})
	if err != nil {
		return nil, err
	}

	// update SignUp
	signUp.CreatedUserID = null.StringFrom(user.ID)
	signUp.CreatedSessionID = null.StringFrom(userSession.ID)
	err = s.signUpRepo.Update(ctx, tx, signUp,
		sqbmodel.SignUpColumns.CreatedUserID,
		sqbmodel.SignUpColumns.CreatedSessionID)
	if err != nil {
		return nil, err
	}

	// increment successful sign-up count for instance
	err = jobs.IncrementSuccessfulSignUpCount(ctx, s.gueClient, jobs.IncrementSuccessfulSignUpCountArgs{
		InstanceID: env.Instance.ID,
		Day:        signUp.CreatedAt.UTC().Format("2006-01-02"),
	}, jobs.WithTx(tx))
	if err != nil {
		return nil, err
	}

	// if we have an external account, verify that the correct verification is attached to it.
	// if not, swap it out.
	var extID string
	if signUp.SuccessfulExternalAccountIdentificationID.Valid {
		extID = signUp.ExternalAccountVerificationID.String
	}

	// add all verified identifications to the user
	verifiedIdentifications, err := s.findUniqVerifiedIdentifications(ctx, tx, signUp)
	if err != nil {
		return nil, err
	}

	userUpdateCols := make([]string, 0)
	var fetchImageIdent *model.Identification
	var hasContactInfoVerified bool
	for _, identification := range verifiedIdentifications {
		updateUserPrimaryIdentifications(user, identification, &userUpdateCols)

		if oauth.ProviderExists(identification.Type) {
			fetchImageIdent = identification
		}

		identificationUpdateCols := []string{sqbmodel.IdentificationColumns.UserID}
		if extID == identification.ID {
			if identification.VerificationID.String != signUp.ExternalAccountVerificationID.String {
				identificationUpdateCols = append(identificationUpdateCols, sqbmodel.IdentificationColumns.VerificationID)
				identification.VerificationID = signUp.ExternalAccountVerificationID
			}
		}

		identification.UserID = null.StringFrom(user.ID)
		err = s.identificationRepo.Update(ctx, tx, identification, identificationUpdateCols...)
		if clerkerrors.IsUniqueConstraintViolation(err, clerkerrors.UniqueIdentification) {
			return nil, apierror.IdentificationExists(identification.Type, err)
		} else if err != nil {
			return nil, err
		}

		if identification.VerificationID.Valid {
			verification, err := s.verificationRepo.FindByID(ctx, tx, identification.VerificationID.String)
			if err != nil {
				return nil, err
			}

			verification.IdentificationID = null.StringFrom(identification.ID)
			err = s.verificationRepo.UpdateIdentificationID(ctx, tx, verification)
			if err != nil {
				return nil, err
			}
		}

		attribute := userSettings.GetAttribute(names.AttributeName(identification.Type))
		if attribute.IsVerifiable() && attribute.UsedAsContactInfo() {
			hasContactInfoVerified = true
		}
	}

	// find possible unverified identifications
	allIdentIDs := make([]string, 0)
	if signUp.EmailAddressID.Valid {
		allIdentIDs = append(allIdentIDs, signUp.EmailAddressID.String)
	}

	if signUp.PhoneNumberID.Valid {
		allIdentIDs = append(allIdentIDs, signUp.PhoneNumberID.String)
	}

	if signUp.Web3WalletID.Valid {
		allIdentIDs = append(allIdentIDs, signUp.Web3WalletID.String)
	}

	unverifiedIdentifications, err := s.identificationRepo.FindAllUnverifiedByIDs(ctx, tx, env.Instance.ID, allIdentIDs...)
	if err != nil {
		return nil, err
	}

	for _, identification := range unverifiedIdentifications {
		identification.UserID = null.StringFrom(user.ID)
		updateCols := set.New[string](sqbmodel.IdentificationColumns.UserID)

		// In case there isn't provided a verified contact info identification (email, phone)
		// then mark any of the provided unverified identifications as reserved. That means that,
		// the newly created user will be able to use those reserved identifications to sign-in.
		// Also, no other user can use them to sign-up or claim them from the UserProfile
		attr := userSettings.GetAttribute(names.AttributeName(identification.Type))
		if userSettings.SignUp.Progressive && !attr.Base().VerifyAtSignUp && attr.IsVerifiable() && !hasContactInfoVerified {
			identification.Status = constants.ISReserved
			updateCols.Insert(sqbmodel.IdentificationColumns.Status)

			updateUserPrimaryIdentifications(user, identification, &userUpdateCols)
		}

		if err = s.identificationRepo.Update(ctx, tx, identification, updateCols.Array()...); err != nil {
			return nil, err
		}
	}

	// Post creation user-updates
	hasAvatarURL := externalAccount != nil && externalAccount.AvatarURL != ""
	if fetchImageIdent != nil && hasAvatarURL {
		// TODO(dimkl): Move FetchOAuthAvatar & imageURL to imageService
		imageID := rand.InternalClerkID(constants.IDPImage)
		if err := jobs.FetchOAuthAvatar(ctx, s.gueClient, jobs.FetchOauthAvatarArgs{
			IdentificationID: fetchImageIdent.ID,
			ImageID:          imageID,
		}, jobs.WithTx(tx)); err != nil {
			return nil, err
		}

		// Generate imageURL
		imageURL, err := s.imageService.PublicURL(fetchImageIdent.Type, imageID)
		if err != nil {
			return nil, err
		}

		// Persist imageURL to DB
		user.ProfileImagePublicURL = null.StringFrom(imageURL)
		userUpdateCols = append(userUpdateCols, sqbmodel.UserColumns.ProfileImagePublicURL)
	}

	if len(userUpdateCols) > 0 {
		if err = s.userRepo.Update(ctx, tx, user, userUpdateCols...); err != nil {
			return nil, err
		}
	}

	if signUp.InstanceInvitationID.Valid {
		invitation := &model.Invitation{
			Invitation: &sqbmodel.Invitation{
				ID:     signUp.InstanceInvitationID.String,
				Status: constants.StatusAccepted,
			},
		}
		err := s.invitationRepo.UpdateStatus(ctx, tx, invitation)
		if err != nil {
			return nil, err
		}
	}

	if err := s.associateExistingOrgInvitation(ctx, tx, verifiedIdentifications, env.Instance.ID, user.ID); err != nil {
		return nil, err
	}

	if err = s.handleOrgDomain(ctx, tx, env.AuthConfig, verifiedIdentifications, env.Instance.ID, user.ID); err != nil {
		return nil, err
	}

	// client changes
	// - remove sign_up
	// - rotate token and cookie value on it because we have a new session
	//
	// The above updates can happen either now or in the next time there is a call by the client.
	// This behaviour is controlled by the `postponeCookieUpdate`.
	// The reason is that the convert to user might be called by a different device (i.e. in the
	// case of magic links). In this case, we need to make sure that the cookie is
	// updated by the device that started the process of session creation, in order to get the
	// updated cookie.
	if postponeCookieUpdate {
		client.PostponeCookieUpdate = true
		cdsClient := client_data.NewClientFromClientModel(client)
		if err := s.clientDataService.UpdateClientPostponeCookieUpdate(ctx, env.Instance.ID, cdsClient); err != nil {
			return nil, fmt.Errorf("signup/convertToUser: updating postpone cookie update flag for client %s: %w", client.ID, err)
		}
		cdsClient.CopyToClientModel(client)
	} else {
		client.SignUpID = null.StringFromPtr(nil)
		clientColumns := []string{client_data.ClientColumns.SignUpID}

		if rotatingTokenNonce != nil {
			client.RotatingTokenNonce = null.StringFromPtr(rotatingTokenNonce)
			clientColumns = append(clientColumns, client_data.ClientColumns.RotatingTokenNonce)
		}

		cdsClient := client_data.NewClientFromClientModel(client)
		if err := s.clientDataService.UpdateClient(ctx, env.Instance.ID, cdsClient, clientColumns...); err != nil {
			return nil, fmt.Errorf("signup/convertToUser: failed to update columns %v for client %s: %w", clientColumns, cdsClient.ID, err)
		}
		cdsClient.CopyToClientModel(client)

		if err := s.cookieService.UpdateClientCookieValue(ctx, env.Instance, client); err != nil {
			return nil, fmt.Errorf("signup/convertToUser: updating client cookie for instance=%s, client=%s: %w", env.Instance.ID, client.ID, err)
		}
	}

	// send user created event
	err = s.sendUserCreatedEvent(ctx, tx, env.Instance, userSettings, user, externalAccount)
	if err != nil {
		return nil, fmt.Errorf("signup/convertToUser: send user created event for %+v in instance %+v: %w", user, env.Instance.ID, err)
	}

	return userSession, nil
}

// TODO refactor this using "event subscription" logic
func (s *Service) sendUserCreatedEvent(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User,
	externalAccount *model.ExternalAccount,
) error {
	userSerializable, err := s.serializableService.ConvertUser(ctx, exec, userSettings, user)
	if err != nil {
		return err
	}

	if err := s.eventService.UserCreated(ctx, exec, instance, userSerializable); err != nil {
		return err
	}

	err = s.gampService.EnqueueEvent(ctx, exec, instance.ID, user.ID, gamp.SignUpEvent, determineProvider(externalAccount))
	if err != nil {
		return fmt.Errorf("signup/service: error enqueuing event for Google analytics: %w", err)
	}

	return nil
}

// checkIdentificationsNotClaimed checks whether any of the identifications is already claimed
func (s *Service) checkIdentificationsNotClaimed(ctx context.Context, exec database.Executor, signUp *model.SignUp) error {
	verifiedIdentifications, err := s.identificationRepo.FindAllVerifiedWithLinkedByID(ctx, exec, signUp.IdentificationIDs()...)
	if err != nil {
		return err
	}
	for _, identification := range verifiedIdentifications {
		if identification.UserID.Valid {
			return fmt.Errorf("signUp/checkIdentificationsNotClaimed: identification %+v is already claimed: %w",
				identification, clerkerrors.ErrIdentificationClaimed)
		}
	}
	return nil
}

func updateUserPrimaryIdentifications(user *model.User, identification *model.Identification, userUpdateCols *[]string) {
	switch identification.Type {
	case constants.ITEmailAddress:
		if !user.PrimaryEmailAddressID.Valid {
			user.PrimaryEmailAddressID = null.StringFrom(identification.ID)
			*userUpdateCols = append(*userUpdateCols, sqbmodel.UserColumns.PrimaryEmailAddressID)
		}
	case constants.ITPhoneNumber:
		if !user.PrimaryPhoneNumberID.Valid {
			user.PrimaryPhoneNumberID = null.StringFrom(identification.ID)
			*userUpdateCols = append(*userUpdateCols, sqbmodel.UserColumns.PrimaryPhoneNumberID)
		}
	case constants.ITWeb3Wallet:
		if !user.PrimaryWeb3WalletID.Valid {
			user.PrimaryWeb3WalletID = null.StringFrom(identification.ID)
			*userUpdateCols = append(*userUpdateCols, sqbmodel.UserColumns.PrimaryWeb3WalletID)
		}
	case constants.ITUsername:
		if !user.UsernameID.Valid {
			user.UsernameID = null.StringFrom(identification.ID)
			*userUpdateCols = append(*userUpdateCols, sqbmodel.UserColumns.UsernameID)
		}
	}
}

// During user sign up, we try to associate any existing pending organization invitations
// with the newly created user. User will be able to accept the organization afterwards
// and from within the app, without the need to click in the link included in the email
func (s *Service) associateExistingOrgInvitation(ctx context.Context, tx database.Tx, verifiedIdents []*model.Identification, instanceID, userID string) error {
	emailAddress := findVerifiedEmailAddress(verifiedIdents)
	if emailAddress == nil {
		// User didn't provide a verified email address, return early
		return nil
	}

	invitations, err := s.orgInvitationRepo.FindAllPendingByInstanceAndEmailAddress(ctx, tx, instanceID, *emailAddress)
	if err != nil {
		return err
	}

	for _, invitation := range invitations {
		invitation.UserID = null.StringFrom(userID)
		if err := s.orgInvitationRepo.UpdateUserID(ctx, tx, invitation); err != nil {
			return err
		}
	}
	return nil
}

// During user sign up, if user has provided a verified email address that matches the domain of a verified
// organization domain, handle the organization invitation or suggestion correspondingly
func (s *Service) handleOrgDomain(ctx context.Context, tx database.Tx, authConfig *model.AuthConfig, verifiedIdents []*model.Identification, instanceID, userID string) error {
	emailAddress := findVerifiedEmailAddress(verifiedIdents)
	if emailAddress == nil {
		// User didn't provide a verified email address, return early
		return nil
	}

	return s.orgDomainService.CreateInvitationsSuggestionsForUserEmail(ctx, tx, authConfig, *emailAddress, instanceID, userID)
}

// findVerifiedEmailAddress iterates over the verified sign up identifications
// and returns the email address if provided
func findVerifiedEmailAddress(verifiedIdents []*model.Identification) *string {
	for _, ident := range verifiedIdents {
		if ident.IsEmailAddress() && ident.IsVerified() {
			return ident.EmailAddress()
		}
	}
	return nil
}

type Status struct {
	// Affected by instance settings
	RequiredFields []string
	OptionalFields []string

	// Affected by what the user provided
	MissingFields       []string
	UnverifiedFields    []string
	MissingRequirements []string
}

// This is essentially the counterpart of checkStatus, but for instances using
// the Progressive Sign Up (PSU) flow. In other words, instances which have
// AuthConfig.UserSettings.SignUp.Progressive set to 'true'.
//
// TODO(2022-05-09, agis): When every instance is migrated to PSU, we should
// remove checkStatus and rename this to checkStatus.
func (s Service) checkProgressiveStatus(ctx context.Context, exec database.Executor, signUp *model.SignUp, userSettings *usersettings.UserSettings) (Status, error) {
	if !userSettings.SignUp.Progressive {
		panic("non-progressive sign up")
	}

	requiredFields := set.New[string]()
	optionalFields := set.New[string]()
	missingFields := set.New[string]()
	unverifiedFields := set.New[string]()
	providedFields := set.New[string]()

	// gather verified identifications and their linked ones
	verifiedIdentifications, err := s.identificationRepo.FindAllVerifiedWithLinkedByID(ctx, exec, signUp.IdentificationIDs()...)
	if err != nil {
		return Status{}, fmt.Errorf("sign-up/checkStatus: finding verified identifications for %+v: %w", signUp, err)
	}

	var (
		hasAtLeastOneIdentifierProvided bool
		hasVerifiedExternalAccount      bool
		hasVerifiedWeb3Account          bool
		hasVerifiedSAMLAccount          bool
	)

	identificationTypes := set.New[string]()
	for _, identification := range verifiedIdentifications {
		if oauth.ProviderExists(identification.Type) {
			hasVerifiedExternalAccount = true
		}

		// TODO(2022-05-26, Haris): Replace with web3.ProviderExists(identification.Type) after
		// we migrate it from an attribute to a provider (similar to oauth)
		if identification.IsWeb3Wallet() {
			hasVerifiedWeb3Account = true
		}

		if identification.IsSAML() {
			hasVerifiedSAMLAccount = true
		}

		identificationTypes.Insert(identification.Type)
	}

	if userSettings.SignUp.CustomActionRequired {
		requiredFields.Insert("custom_action")

		if !signUp.CustomAction {
			missingFields.Insert("custom_action")
		}
	}

	if signUp.SamlConnectionID.Valid {
		requiredFields.Insert(names.SAML)

		if !hasVerifiedSAMLAccount {
			missingFields.Insert(names.SAML)
		}
	}

	// populate required_fields, optional_fields and unverified_fields with some
	// initial values
	for _, attribute := range userSettings.EnabledAttributes() {
		attr, ok := usersettings.ToSignUpAttribute(attribute)
		if !ok {
			continue
		}

		isVerifiable := attribute.IsVerifiable()
		isProvided := attr.IsProvidedInSignUp(signUp)
		isVerified := identificationTypes.Contains(attribute.Name())

		if attribute.Base().Required {
			requiredFields.Insert(attribute.Name())

			if !isProvided {
				missingFields.Insert(attribute.Name())
			}
		} else {
			optionalFields.Insert(attribute.Name())
		}

		if isVerifiable && isProvided && !isVerified && attribute.Base().VerifyAtSignUp {
			unverifiedFields.Insert(attribute.Name())
		}

		if isProvided {
			providedFields.Insert(attribute.Name())

			if attribute.UsedForIdentification() {
				hasAtLeastOneIdentifierProvided = true
			}
		}
	}

	for _, social := range userSettings.EnabledSocial() {
		if social.Required {
			requiredFields.Insert(social.Strategy)
		} else {
			optionalFields.Insert(social.Strategy)
		}

		if identificationTypes.Contains(social.Strategy) {
			hasAtLeastOneIdentifierProvided = true
		} else if social.Required {
			missingFields.Insert(social.Strategy)
		}
	}

	// having all attributes that can be used as identifiers optional
	// is a special case, as we use it to model the `x OR y OR z`
	everyIdentifierOptional := true
	for _, required := range requiredFields.Array() {
		attr := userSettings.GetAttribute(names.AttributeName(required))
		if attr.UsedForIdentification() {
			everyIdentifierOptional = false
			break
		}
	}

	// If all attributes that can be used as identifiers are
	// optional, we consider that the user wants to model
	// `x OR y OR z`.
	// In this case, we require at least one of them to be provided
	// in order to continue.
	// If none of them is provided, we add all of them into the missing fields.
	if everyIdentifierOptional && !hasAtLeastOneIdentifierProvided {
		for _, optional := range optionalFields.Array() {
			attr := userSettings.GetAttribute(names.AttributeName(optional))
			if attr.UsedForIdentification() {
				missingFields.Insert(optional)
			}

			if social, ok := userSettings.Social[optional]; ok && social.Authenticatable {
				missingFields.Insert(optional)
			}
		}
	}

	//
	// From now on we handle some special cases...
	//

	// Special case #1: When signing up with OAuth, we treat passwords as
	// non-required, even if they are marked as required by the instance
	// settings.
	//
	// If we ever want to support really required passwords (i.e.
	// you're asked to pick a password before the sign up flow can finish),
	// we'll have to revisit this.
	if hasVerifiedExternalAccount || hasVerifiedWeb3Account || hasVerifiedSAMLAccount {
		missingFields.Remove(string(names.Password))
	}

	//
	// Now that we're finished with missing_fields and unverified_fields, we can
	// finally populate missing_requirements too
	//
	missingRequirements := set.New[string]()
	missingRequirements.Insert(missingFields.Array()...)
	missingRequirements.Insert(unverifiedFields.Array()...)

	return Status{
		RequiredFields:      requiredFields.Array(),
		OptionalFields:      optionalFields.Array(),
		MissingFields:       missingFields.Array(),
		UnverifiedFields:    unverifiedFields.Array(),
		MissingRequirements: missingRequirements.Array(),
	}, nil
}

// checkStatus returns the status of the current sign-up.
// This status includes:
// * the `required fields` that need to be supplied so that the sign-up fulfills the attached registration policy
// * the `optional fields` that could be supplied but their absence doesn't block the fulfillment of the sign-up
// * the `missing fields` that need to supplied because their absence blocks the fulfillment of the sign-up
// * the `unverified fields` that have been supplied but they need to be verified, i.e. email address or phone number
// * the `missing requirements` which are the required fields that are still missing from the sign-up
func (s *Service) checkStatus(ctx context.Context, exec database.Executor, signUp *model.SignUp, userSettings *usersettings.UserSettings) (Status, error) {
	status := Status{
		RequiredFields:      make([]string, 0),
		OptionalFields:      make([]string, 0),
		MissingFields:       make([]string, 0),
		UnverifiedFields:    make([]string, 0),
		MissingRequirements: make([]string, 0),
	}

	firstName := userSettings.GetAttribute(names.FirstName)
	lastName := userSettings.GetAttribute(names.LastName)

	firstNameRequired := firstName.Base().Required
	lastNameRequired := lastName.Base().Required
	s.addAttributeToStatus(&status, param.FirstName.Name, firstName)
	s.addAttributeToStatus(&status, param.LastName.Name, lastName)
	if firstNameRequired && !signUp.FirstName.Valid {
		status.MissingFields = append(status.MissingFields, constants.ACAFirstName)
		status.MissingRequirements = append(status.MissingRequirements, constants.ACAFirstName)
	}
	if lastNameRequired && !signUp.LastName.Valid {
		status.MissingFields = append(status.MissingFields, constants.ACALastName)
		status.MissingRequirements = append(status.MissingRequirements, constants.ACALastName)
	}

	//
	// TODO Braden: Go through each page and make sure everything is fulfilled.
	// right now it only goes through one page.
	//
	// TODO Braden: Password Conditions are ignored, and we cheat to ignore pw
	// if theres a googAcc
	//
	requirements := userSettings.WeirdDeprecatedIdentificationRequirementsDoubleArray()[0]
	requirementsSet := set.New(requirements...)
	verifiedIdentifierSet := set.New[string]()
	satisfiedIdentificationRequirements := false
	hasExternalAccountIdentifications := false

	verifiedIdentifications, err := s.identificationRepo.FindAllVerifiedWithLinkedByID(ctx, exec, signUp.IdentificationIDs()...)
	if err != nil {
		return status, fmt.Errorf("sign-up/checkStatus: finding verified identifications for %+v: %w", signUp, err)
	}

	for _, ident := range verifiedIdentifications {
		if oauth.ProviderExists(ident.Type) {
			hasExternalAccountIdentifications = true
		}

		if requirementsSet.Contains(ident.Type) {
			satisfiedIdentificationRequirements = true
		}

		verifiedIdentifierSet.Insert(ident.ID)
	}

	emailRequired := requirementsSet.Contains(constants.ITEmailAddress)
	phoneRequired := requirementsSet.Contains(constants.ITPhoneNumber)
	emailOrPhoneRequired := emailRequired && phoneRequired
	emailCollected := signUp.EmailAddressID.Valid
	phoneCollected := signUp.PhoneNumberID.Valid
	web3WalletCollected := signUp.Web3WalletID.Valid

	if emailOrPhoneRequired {
		status.RequiredFields = append(status.RequiredFields, param.EmailAddressOrPhoneNumber.Name)
		if !emailCollected && !phoneCollected {
			status.MissingFields = append(status.MissingFields, param.EmailAddressOrPhoneNumber.Name)
		}
	} else if emailRequired {
		status.RequiredFields = append(status.RequiredFields, param.EmailAddress.Name)
		if !emailCollected {
			status.MissingFields = append(status.MissingFields, param.EmailAddress.Name)
		}
	} else if phoneRequired {
		status.RequiredFields = append(status.RequiredFields, param.PhoneNumber.Name)
		if !phoneCollected {
			status.MissingFields = append(status.MissingFields, param.PhoneNumber.Name)
		}
	}

	if emailCollected && !verifiedIdentifierSet.Contains(signUp.EmailAddressID.String) {
		status.UnverifiedFields = append(status.UnverifiedFields, constants.ITEmailAddress)
	}

	if phoneCollected && !verifiedIdentifierSet.Contains(signUp.PhoneNumberID.String) {
		status.UnverifiedFields = append(status.UnverifiedFields, constants.ITPhoneNumber)
	}

	if web3WalletCollected && !verifiedIdentifierSet.Contains(signUp.Web3WalletID.String) {
		status.UnverifiedFields = append(status.UnverifiedFields, constants.ITWeb3Wallet)
	}

	username := userSettings.GetAttribute(names.Username)
	if username.Base().Enabled {
		s.addAttributeToStatus(&status, param.Username.Name, username)
		if username.Base().Required && !signUp.UsernameID.Valid && !hasExternalAccountIdentifications {
			status.MissingFields = append(status.MissingFields, param.Username.Name)
			status.MissingRequirements = append(status.MissingRequirements, param.Username.Name)
		}
	}

	password := userSettings.GetAttribute(names.Password)
	s.addAttributeToStatus(&status, param.Password.Name, password)
	if password.Base().Required {
		if !signUp.PasswordDigest.Valid && !hasExternalAccountIdentifications {
			status.MissingFields = append(status.MissingFields, param.Password.Name)
			status.MissingRequirements = append(status.MissingRequirements, constants.ACAPassword)
		}
	}

	if !satisfiedIdentificationRequirements {
		status.MissingRequirements = append(status.MissingRequirements, requirements...)
	}

	return status, nil
}

func (s *Service) addAttributeToStatus(status *Status, fieldName string, attribute usersettings.Attribute) {
	if attribute.Base().Required {
		status.RequiredFields = append(status.RequiredFields, fieldName)
	} else if attribute.Base().Enabled {
		status.OptionalFields = append(status.OptionalFields, fieldName)
	}
}

func (s *Service) ConvertToSerializable(
	ctx context.Context,
	exec database.Executor,
	signUp *model.SignUp,
	userSettings *usersettings.UserSettings,
	externalAuthorizationURL string,
) (*model.SignUpSerializable, error) {
	signUpSerializable := model.SignUpSerializable{SignUp: signUp}

	var signUpStatus Status
	var err error
	if userSettings.SignUp.Progressive {
		signUpStatus, err = s.checkProgressiveStatus(ctx, exec, signUp, userSettings)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: missing requirements for %+v: %w", signUp, err)
		}
	} else {
		signUpStatus, err = s.checkStatus(ctx, exec, signUp, userSettings)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: missing requirements for %+v: %w", signUp, err)
		}
	}

	signUpSerializable.CustomAction = signUp.CustomAction
	signUpSerializable.RequiredFields = signUpStatus.RequiredFields
	signUpSerializable.OptionalFields = signUpStatus.OptionalFields
	signUpSerializable.MissingFields = signUpStatus.MissingFields
	signUpSerializable.UnverifiedFields = signUpStatus.UnverifiedFields

	signUpSerializable.FirstName = signUp.FirstName.Ptr()
	signUpSerializable.LastName = signUp.LastName.Ptr()

	if signUp.UsernameID.Valid {
		identification, err := s.identificationRepo.FindByID(ctx, exec, signUp.UsernameID.String)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: find username identification %s for %+v: %w",
				signUp.UsernameID.String, signUp, err)
		}

		signUpSerializable.Username = identification.Username()
	}

	if signUp.EmailAddressID.Valid {
		identification, err := s.identificationRepo.QueryByID(ctx, exec, signUp.EmailAddressID.String)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: find email address identification %s for %+v: %w",
				signUp.EmailAddressID.String, signUp, err)
		}
		if identification != nil {
			signUpSerializable.EmailAddress = identification.EmailAddress()

			if identification.VerificationID.Valid {
				signUpSerializable.EmailAddressVerification, err = s.verificationService.VerificationWithStatus(
					ctx, exec, identification.VerificationID.String)
				if err != nil {
					return nil, fmt.Errorf("signUp/convertToSerializable: find verification for identification %+v: %w",
						identification, err)
				}
			}
		}
	}

	if signUp.PhoneNumberID.Valid {
		identification, err := s.identificationRepo.QueryByID(ctx, exec, signUp.PhoneNumberID.String)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: find phone number identification %s for %+v: %w",
				signUp.PhoneNumberID.String, signUp, err)
		}
		if identification != nil {
			signUpSerializable.PhoneNumber = identification.PhoneNumber()

			if identification.VerificationID.Valid {
				signUpSerializable.PhoneNumberVerification, err = s.verificationService.VerificationWithStatus(
					ctx, exec, identification.VerificationID.String)
				if err != nil {
					return nil, fmt.Errorf("signUp/convertToSerializable: find verification for identification %+v: %w",
						identification, err)
				}
			}
		}
	}

	if signUp.Web3WalletID.Valid {
		identification, err := s.identificationRepo.QueryByID(ctx, exec, signUp.Web3WalletID.String)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: find web3 wallet identification %s for %+v: %w",
				signUp.Web3WalletID.String, signUp, err)
		}
		if identification != nil {
			signUpSerializable.Web3Wallet = identification.Web3Wallet()

			if identification.VerificationID.Valid {
				signUpSerializable.Web3WalletVerification, err = s.verificationService.VerificationWithStatus(
					ctx, exec, identification.VerificationID.String)
				if err != nil {
					return nil, fmt.Errorf("signUp/convertToSerializable: find verification for identification %+v: %w",
						identification, err)
				}
			}
		}
	}

	if signUp.ExternalAccountVerificationID.Valid {
		signUpSerializable.ExternalAccountVerification, err = s.verificationService.VerificationWithStatus(
			ctx, exec, signUp.ExternalAccountVerificationID.String)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: find verification for external account verification %s: %w",
				signUp.ExternalAccountVerificationID.String, err)
		}

		if externalAuthorizationURL != "" {
			signUpSerializable.ExternalAccountVerification.ExternalAuthorizationURL = null.StringFrom(externalAuthorizationURL)
		}
	}

	if signUp.SuccessfulExternalAccountIdentificationID.Valid {
		identification, err := s.identificationRepo.FindByID(ctx, exec, signUp.SuccessfulExternalAccountIdentificationID.String)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: find successful external account identification for %+v: %w",
				signUp, err)
		}

		signUpSerializable.SuccessfulExternalAccountIdentification, err = s.serializableService.ConvertIdentification(ctx, exec, identification)
		if err != nil {
			return nil, fmt.Errorf("signUp/convertToSerializable: serializing identification %+v: %w", identification, err)
		}
	}

	return &signUpSerializable, nil
}

func determineProvider(externalAccount *model.ExternalAccount) string {
	if externalAccount == nil {
		return "clerk"
	}

	return externalAccount.Provider
}

// NewSignUpAwareContext returns a context with the signUp object set under
// the ctxkeys.SignUp key.
func NewSignUpAwareContext(ctx context.Context, signUp *model.SignUp) context.Context {
	return context.WithValue(ctx, ctxkeys.SignUp, signUp)
}

// FromContext retrieves the *model.SignUp from the passed in context.
func FromContext(ctx context.Context) *model.SignUp {
	return ctx.Value(ctxkeys.SignUp).(*model.SignUp)
}

// FinalizeFlowParams holds all the key models that are needed to complete a
// SignUp.
type FinalizeFlowParams struct {
	SignUp               *model.SignUp
	Env                  *model.Env
	Client               *model.Client
	ExternalAccount      *model.ExternalAccount
	SAMLAccount          *model.SAMLAccount
	UserSettings         *usersettings.UserSettings
	PostponeCookieUpdate bool
	RotatingTokenNonce   *string
}

// FinalizeFlow will attempt to complete a SignUp, using all models passed with FinalizeSignUp.
// We need to check that the SignUp's identification doesn't belong to another user and
// then attempt to transition the SignUp to complete and create a new session and user.
// If an identification was provided, it will be marked as verified.
func (s *Service) FinalizeFlow(ctx context.Context, tx database.Tx, finalize FinalizeFlowParams) (*model.Session, error) {
	signUp := finalize.SignUp
	externalAccount := finalize.ExternalAccount
	samlAccount := finalize.SAMLAccount

	// If there is an external account, update sign up with missing info (first name, last name, username)
	if externalAccount != nil {
		err := s.updateSignUpFromExternalAccount(ctx, tx, signUp, externalAccount, finalize.UserSettings.SignUp.Progressive)
		if err != nil {
			return nil, fmt.Errorf("signUp/Complete: cannot update sign up %s from external account: %w", signUp.ID, err)
		}
	}

	// If there is a SAML account, update sign up with missing info if any
	if samlAccount != nil {
		err := s.updateSignUpFromSAMLAccount(ctx, tx, signUp, samlAccount)
		if err != nil {
			return nil, fmt.Errorf("signUp/Complete: cannot update sign up %s from SAML account: %w", signUp.ID, err)
		}
	}

	// Check whether the given identifiers are allowed based on the instance
	// restrictions (i.e. allowlists and blocklists).
	restrictionsCheck, err := s.checkRestrictions(ctx, tx, finalize.UserSettings, signUp, finalize.Env.AuthConfig.TestMode)
	if err != nil {
		return nil, fmt.Errorf("signUp/Complete: cannot check allowlist for sign up %s: %w", signUp.ID, err)
	}
	if !restrictionsCheck.Allowed {
		return nil, apierror.IdentifierNotAllowedAccess(restrictionsCheck.Offenders...)
	}

	// Check that the identification is not already used.
	err = s.checkIdentificationsNotClaimed(ctx, tx, signUp)
	if err != nil {
		return nil, fmt.Errorf("signUp/Complete: one or more identifications from %+v are already claimed: %w",
			signUp, err)
	}

	// Check if sign up can transition to complete.
	userSettings := usersettings.NewUserSettings(finalize.Env.AuthConfig.UserSettings)

	var signUpStatus Status
	if userSettings.SignUp.Progressive {
		signUpStatus, err = s.checkProgressiveStatus(ctx, tx, signUp, userSettings)
		if err != nil {
			return nil, fmt.Errorf("signUp/Complete: %w", err)
		}
	} else {
		signUpStatus, err = s.checkStatus(ctx, tx, signUp, userSettings)
		if err != nil {
			return nil, fmt.Errorf("signUp/Complete: %w", err)
		}
	}
	// Still have missing requirements, can't convert to a new session.
	if len(signUpStatus.MissingRequirements) != 0 {
		return nil, nil
	}

	// NOTE(2022-05-03, agis): even though we have Progressive Sign Up now, we
	// leave this on for backwards compatibility reasons
	//
	// TODO: Revisit the following when we implement a way to handle missing sign-up requirements
	// Attempt to set a username in the case that usernames are required (failing to do so would cause the flow to fail).
	// Logic:
	// * get username from external account (if the provider supports it)
	// * fallback to the local part of the email ONLY IF username is required
	if userSettings.GetAttribute(names.Username).Base().Enabled && !signUp.UsernameID.Valid && externalAccount != nil {
		username := ""

		if externalAccount.Username.Valid {
			username = externalAccount.Username.String
		} else if userSettings.GetAttribute(names.Username).Base().Required {
			username = emailaddress.LocalPart(externalAccount.EmailAddress)
		}

		if username != "" {
			if err := s.assignUsername(ctx, tx, username, signUp); err != nil {
				return nil, fmt.Errorf("signUp/finalizeFlow: assigning username based on %s on sign up %s: %w",
					username, signUp.ID, err)
			}
		}
	}
	return s.convertToUser(
		ctx,
		tx,
		finalize.Client,
		finalize.Env,
		signUp,
		externalAccount,
		finalize.PostponeCookieUpdate,
		finalize.RotatingTokenNonce,
	)
}

func (s Service) updateSignUpFromExternalAccount(ctx context.Context, exec database.Executor, signUp *model.SignUp, externalAccount *model.ExternalAccount, isProgressiveSignUp bool) error {
	signUpUpdateColumns := make([]string, 0)

	if signUp.SuccessfulExternalAccountIdentificationID.Valid {
		// if an email was provided (verified or not) as part of this flow, update signUp to reflect this
		externalAccountIdent, err := s.identificationRepo.FindByIDAndInstance(ctx, exec, signUp.SuccessfulExternalAccountIdentificationID.String, signUp.InstanceID)
		if err != nil {
			return err
		}

		if externalAccountIdent.TargetIdentificationID.Valid && !signUp.EmailAddressID.Valid {
			emailIdent, err := s.identificationRepo.QueryEmailByID(ctx, exec, externalAccountIdent.TargetIdentificationID.String)
			if err != nil {
				return err
			}

			if emailIdent != nil {
				signUp.EmailAddressID = null.StringFrom(emailIdent.ID)
				signUpUpdateColumns = append(signUpUpdateColumns, sqbmodel.SignUpColumns.EmailAddressID)
			}
		}
	}

	if isProgressiveSignUp {
		if !signUp.FirstName.Valid && externalAccount.FirstName != "" {
			signUp.FirstName = null.StringFrom(externalAccount.FirstName)
			signUpUpdateColumns = append(signUpUpdateColumns, sqbmodel.SignUpColumns.FirstName)
		}

		if !signUp.LastName.Valid && externalAccount.LastName != "" {
			signUp.LastName = null.StringFrom(externalAccount.LastName)
			signUpUpdateColumns = append(signUpUpdateColumns, sqbmodel.SignUpColumns.LastName)
		}
	} else {
		if !signUp.FirstName.Valid {
			signUp.FirstName = null.StringFrom(externalAccount.FirstName)
			signUpUpdateColumns = append(signUpUpdateColumns, sqbmodel.SignUpColumns.FirstName)
		}

		if !signUp.LastName.Valid {
			signUp.LastName = null.StringFrom(externalAccount.LastName)
			signUpUpdateColumns = append(signUpUpdateColumns, sqbmodel.SignUpColumns.LastName)
		}
	}

	// TODO(oauth): We should probably do this inside the external_account.Create() service method, but we
	// don't support currently any other identification type rather than email address
	if !signUp.UsernameID.Valid && externalAccount.Username.Valid {
		username := externalAccount.Username.String

		apiErr, err := s.validatorService.ValidateUsername(ctx, exec, username, signUp.InstanceID)
		if err != nil {
			return err
		}

		// We create the username identification and assign it to the sign up, if the OAuth username satisfies the
		// requirements and isn't taken by any other instance user
		if apiErr == nil {
			usernameIdentification, err := s.identificationService.CreateUsername(ctx, exec, username, nil, signUp.InstanceID)
			if err != nil {
				return err
			}

			signUp.UsernameID = null.StringFrom(usernameIdentification.ID)
			signUpUpdateColumns = append(signUpUpdateColumns, sqbmodel.SignUpColumns.UsernameID)
		}
	}

	return s.signUpRepo.Update(ctx, exec, signUp, signUpUpdateColumns...)
}

func (s Service) updateSignUpFromSAMLAccount(ctx context.Context, exec database.Executor, signUp *model.SignUp, samlAccount *model.SAMLAccount) error {
	updateCols := make([]string, 0)

	if signUp.SuccessfulExternalAccountIdentificationID.Valid {
		// if an email was provided (verified or not) as part of this flow, update signUp to reflect this
		samlAccountIdent, err := s.identificationRepo.FindByIDAndInstance(ctx, exec, signUp.SuccessfulExternalAccountIdentificationID.String, signUp.InstanceID)
		if err != nil {
			return err
		}

		if samlAccountIdent.TargetIdentificationID.Valid && !signUp.EmailAddressID.Valid {
			emailIdent, err := s.identificationRepo.FindEmailByID(ctx, exec, samlAccountIdent.TargetIdentificationID.String)
			if err != nil {
				return err
			}

			signUp.EmailAddressID = null.StringFrom(emailIdent.ID)
			updateCols = append(updateCols, sqbmodel.SignUpColumns.EmailAddressID)
		}
	}

	if !signUp.FirstName.Valid && samlAccount.FirstName.Valid {
		signUp.FirstName = samlAccount.FirstName
		updateCols = append(updateCols, sqbmodel.SignUpColumns.FirstName)
	}

	if !signUp.LastName.Valid && samlAccount.LastName.Valid {
		signUp.LastName = samlAccount.LastName
		updateCols = append(updateCols, sqbmodel.SignUpColumns.LastName)
	}

	signUpPublicMetadata := json.RawMessage(signUp.PublicMetadata)
	samlAccountPublicMetadata := json.RawMessage(samlAccount.PublicMetadata)
	if len(signUpPublicMetadata) > 0 && !reflect.DeepEqual(signUpPublicMetadata, samlAccountPublicMetadata) {
		merged, err := clerkjson.Patch(signUpPublicMetadata, samlAccountPublicMetadata)
		if err != nil {
			return err
		}
		signUp.PublicMetadata = types.JSON(merged)
		updateCols = append(updateCols, sqbmodel.SignUpColumns.PublicMetadata)
	}

	return s.signUpRepo.Update(ctx, exec, signUp, updateCols...)
}

func (s *Service) assignUsername(ctx context.Context, exec database.Executor, username string, signUp *model.SignUp) error {
	var possibleUsernames []string
	possibleUsernames = append(possibleUsernames, username)
	for suffix := 1; suffix < 10; suffix++ {
		possibleUsernames = append(possibleUsernames, fmt.Sprintf("%s_%d", username, suffix))
	}

	availableUsernames, err := s.identificationRepo.FindAllUnverifiedFromGivenIdentifiers(ctx, exec, constants.ITUsername, signUp.InstanceID, possibleUsernames...)
	if err != nil {
		return fmt.Errorf("signUp/assignUsername: finding all unverified usernames from %v in instance %s: %w",
			possibleUsernames, signUp.InstanceID, err)
	}

	var finalUsername string
	if len(availableUsernames) == 0 {
		finalUsername = rand.InternalClerkID(username)
	} else {
		finalUsername = availableUsernames[0]
	}

	usernameIdentification, err := s.identificationService.CreateUsername(ctx, exec, finalUsername, nil, signUp.InstanceID)
	if err != nil {
		return fmt.Errorf("signUp/assignUsername: creating username %s on instance %s: %w",
			username, signUp.InstanceID, err)
	}

	signUp.UsernameID = null.StringFrom(usernameIdentification.ID)
	if err := s.signUpRepo.UpdateUsernameID(ctx, exec, signUp); err != nil {
		return fmt.Errorf("signUp/assignUsername: updating username id %s on sign up %s: %w",
			usernameIdentification.ID, signUp.ID, err)
	}
	return nil
}

type checkRestrictionsResult struct {
	Allowed   bool
	Offenders []string
}

func (s *Service) checkRestrictions(
	ctx context.Context,
	exec database.Executor,
	userSettings *usersettings.UserSettings,
	signUp *model.SignUp,
	testMode bool,
) (checkRestrictionsResult, error) {
	res := checkRestrictionsResult{}

	restrictionsEnabled := userSettings.Restrictions.Allowlist.Enabled || userSettings.Restrictions.Blocklist.Enabled
	if restrictionsEnabled && !hasRestrictionAwareFirstFactor(userSettings) {
		// Return early if all first factors are not restriction aware and there's
		// at least one field populated on the sign up.
		for _, attribute := range userSettings.EnabledAttributes() {
			if !userSettings.IsUsedForFirstFactor(names.AttributeName(attribute.Name())) {
				continue
			}
			if fieldForAttribute(signUp, names.AttributeName(attribute.Name())).Valid {
				return res, nil
			}
		}
	}

	idents, err := s.identificationRepo.FindAllByID(ctx, exec, restrictionIdentificationIDs(signUp)...)
	if err != nil {
		return res, err
	}
	if len(idents) == 0 {
		res.Allowed = true
		return res, nil
	}
	identifiers := make([]restrictions.Identification, len(idents))
	for i, ident := range idents {
		identifiers[i] = restrictions.Identification{
			Identifier:          ident.Identifier.String,
			CanonicalIdentifier: ident.CanonicalIdentifier.String,
			Type:                ident.Type,
		}
	}
	checkAll, err := s.restrictionService.CheckAll(
		ctx,
		exec,
		identifiers,
		restrictions.Settings{
			Restrictions: userSettings.Restrictions,
			TestMode:     testMode,
		},
		signUp.InstanceID,
	)
	if err != nil {
		return res, nil
	}
	res.Allowed = checkAll.HasAtLeastOneAllowed() && !checkAll.HasAtLeastOneBlocked()
	if checkAll.HasAtLeastOneBlocked() {
		res.Offenders = append(res.Offenders, checkAll.Blocked()...)
	}
	return res, nil
}

// Only these attributes make sense as restriction identifiers.
var restrictionAwareAttributes = []names.AttributeName{names.EmailAddress, names.PhoneNumber, names.Web3Wallet}

// Returns the sign up field that is associated with the attribute
// with the provided name.
func fieldForAttribute(signUp *model.SignUp, name names.AttributeName) null.String {
	switch name {
	case names.EmailAddress:
		return signUp.EmailAddressID
	case names.PhoneNumber:
		return signUp.PhoneNumberID
	case names.Username:
		return signUp.UsernameID
	case names.Web3Wallet:
		return signUp.Web3WalletID
	default:
		return null.NewString("", false)
	}
}

// Returns the sign up identification IDs for all the identifications
// that can be used for restrictions, i.e. allowlists and blocklists.
func restrictionIdentificationIDs(signUp *model.SignUp) []string {
	ids := make([]string, 0)
	for _, name := range restrictionAwareAttributes {
		id := fieldForAttribute(signUp, name)
		if id.Valid {
			ids = append(ids, id.String)
		}
	}
	return ids
}

// hasRestrictionAwareFirstFactor returns true if the user settings are properly
// configured to support restrictions, i.e. allowlists and blocklists.
// There must be at least one attribute used for first factor
// that can be used as restriction identifier.
func hasRestrictionAwareFirstFactor(userSettings *usersettings.UserSettings) bool {
	for _, name := range restrictionAwareAttributes {
		if userSettings.IsUsedForFirstFactor(name) {
			return true
		}
	}
	return false
}

func (s *Service) findUniqVerifiedIdentifications(ctx context.Context, exec database.Executor, signUp *model.SignUp) ([]*model.Identification, error) {
	allVerifiedIdentifications, err := s.identificationRepo.FindAllVerifiedWithLinkedByID(ctx, exec, signUp.IdentificationIDs()...)
	if err != nil {
		return nil, err
	}

	// Discard duplicates (same identifier and type). Be careful
	// to preserve any identifications without identifier.
	uniqIdents := map[string]*model.Identification{}
	for _, ident := range allVerifiedIdentifications {
		if ident.Identifier.Valid {
			uniqIdents[ident.Identifier.String+ident.Type] = ident
		} else {
			uniqIdents[ident.ID] = ident
		}
	}
	identifications := make([]*model.Identification, len(uniqIdents))
	i := 0
	for _, v := range uniqIdents {
		identifications[i] = v
		i++
	}
	return identifications, nil
}
