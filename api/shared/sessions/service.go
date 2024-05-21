package sessions

import (
	"context"
	"fmt"
	"time"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/billing"
	"clerk/api/shared/client_data"
	"clerk/api/shared/events"
	"clerk/api/shared/gamp"
	"clerk/api/shared/organizations"
	"clerk/api/shared/serializable"
	"clerk/api/shared/session_activities"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/maintenance"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock     clockwork.Clock
	gueClient *gue.Client
	db        database.Database

	// services
	billingService           *billing.Service
	eventService             *events.Service
	gampService              *gamp.Service
	orgService               *organizations.Service
	serializableService      *serializable.Service
	clientDataService        *client_data.Service
	sessionActivitiesService *session_activities.Service

	// repositories
	actorTokenRepo        *repository.ActorToken
	identificationRepo    *repository.Identification
	integrationRepo       *repository.Integrations
	orgMembershipRepo     *repository.OrganizationMembership
	sessionRepo           *repository.Sessions
	sessionActivitiesRepo *repository.SessionActivities
	signInRepo            *repository.SignIn
	signUpRepo            *repository.SignUp
	userRepo              *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                    deps.Clock(),
		gueClient:                deps.GueClient(),
		db:                       deps.DB(),
		billingService:           billing.NewService(deps),
		eventService:             events.NewService(deps),
		gampService:              gamp.NewService(deps),
		orgService:               organizations.NewService(deps),
		serializableService:      serializable.NewService(deps.Clock()),
		actorTokenRepo:           repository.NewActorToken(),
		identificationRepo:       repository.NewIdentification(),
		integrationRepo:          repository.NewIntegrations(),
		orgMembershipRepo:        repository.NewOrganizationMembership(),
		sessionRepo:              repository.NewSessions(deps.Clock()),
		sessionActivitiesRepo:    repository.NewSessionActivities(),
		signInRepo:               repository.NewSignIn(),
		signUpRepo:               repository.NewSignUp(),
		userRepo:                 repository.NewUsers(),
		clientDataService:        client_data.NewService(deps),
		sessionActivitiesService: session_activities.NewService(),
	}
}

type CreateParams struct {
	AuthConfig           *model.AuthConfig
	Instance             *model.Instance
	ClientID             string
	User                 *model.User
	ActivityID           *string
	ExternalAccount      *model.ExternalAccount
	ActorTokenID         *string
	ActiveOrganizationID *string
	SessionStatus        *string
}

func (s *Service) Create(
	ctx context.Context,
	exec database.Executor,
	params CreateParams) (*model.Session, error) {
	if params.User.Banned {
		return nil, apierror.InvalidAuthorization()
	}

	latestSession, err := s.clientDataService.QuerySessionsLatestTouchedByUser(ctx, params.Instance.ID, params.User.ID)
	if err != nil {
		return nil, fmt.Errorf("sessions/create: querying latest touched by user %s: %w",
			params.User.ID, err)
	}

	activeOrganizationID := null.StringFromPtr(nil)
	if params.ActiveOrganizationID != nil {
		activeOrganizationID = null.StringFromPtr(params.ActiveOrganizationID)
	} else if latestSession != nil {
		activeOrganizationID = latestSession.ActiveOrganizationID
	}

	now := s.clock.Now().UTC()
	sessionStatus := constants.SESSActive
	if params.SessionStatus != nil {
		sessionStatus = *params.SessionStatus
	}
	session := &model.Session{Session: &sqbmodel.Session{
		InstanceID:               params.Instance.ID,
		ClientID:                 params.ClientID,
		UserID:                   params.User.ID,
		ActiveOrganizationID:     activeOrganizationID,
		TouchedAt:                now,
		Status:                   sessionStatus,
		ExpireAt:                 now.Add(time.Second * time.Duration(params.AuthConfig.SessionSettings.TimeToExpire)),
		AbandonAt:                now.Add(time.Second * time.Duration(params.AuthConfig.SessionSettings.TimeToAbandon)),
		SessionInactivityTimeout: params.AuthConfig.SessionSettings.InactivityTimeout,
		SessionActivityID:        null.StringFromPtr(params.ActivityID),
	}}

	if params.ActorTokenID != nil {
		actorToken, err := s.actorTokenRepo.FindByID(ctx, exec, *params.ActorTokenID)
		if err != nil {
			return nil, fmt.Errorf("sessions/create: looking for actor token %s: %w", *params.ActorTokenID, err)
		}

		session.Actor = null.JSONFrom(actorToken.Actor)
		session.SessionInactivityTimeout = constants.ExpiryTimeTransactional
		session.ExpireAt = s.clock.Now().UTC().Add(time.Duration(actorToken.SessionMaxDurationInSeconds) * time.Second)
	}

	cdsSession := client_data.NewSessionFromSessionModel(session)
	if err := s.clientDataService.CreateSession(ctx, params.Instance.ID, params.ClientID, cdsSession); err != nil {
		return nil, err
	}
	cdsSession.CopyToSessionModel(session)

	// Update user's last_sign_in_at timestamp if not impersonation session
	if !session.HasActor() {
		if err := s.userRepo.UpdateLastSignInAtByID(ctx, exec, params.User.ID, session.CreatedAt); err != nil {
			return nil, fmt.Errorf("sessions/create: updating user last_sign_in_at %v: %w", session.CreatedAt, err)
		}
	}

	if params.ActivityID != nil {
		err := s.sessionActivitiesRepo.UpdateSessionID(ctx, exec, *params.ActivityID, session.ID)
		if err != nil {
			return nil, fmt.Errorf("sessions/create: updating session id %s in session activity %s: %w",
				session.ID, *params.ActivityID, err)
		}
	}

	if session.Status == constants.SESSActive {
		err = s.eventService.SessionCreated(ctx, exec, params.Instance, session)
		if err != nil {
			return nil, fmt.Errorf("sessions/create: send session created event for %+v in instance %+v: %w",
				session, params.Instance.ID, err)
		}
	}

	err = s.gampService.EnqueueEvent(ctx, exec, params.Instance.ID, session.UserID, gamp.LoginEvent, determineProvider(params.ExternalAccount))
	if err != nil {
		return nil, fmt.Errorf("sessions/create: enqueing gamp event %s for %s: %w",
			string(gamp.LoginEvent), session.UserID, err)
	}

	return session, nil
}

func (s *Service) Activate(ctx context.Context, instance *model.Instance, session *model.Session) error {
	if session.Status != constants.SESSPendingActivation {
		return clerkerrors.WithStacktrace("invalid session status: %s", session.Status)
	}
	cdsSession := client_data.NewSessionFromSessionModel(session)
	cdsSession.Status = constants.SESSActive
	if err := s.clientDataService.UpdateSessionStatus(ctx, cdsSession); err != nil {
		return err
	}
	cdsSession.CopyToSessionModel(session)
	if err := s.eventService.SessionCreated(ctx, s.db, instance, session); err != nil {
		return fmt.Errorf("sessions/activate: send session created event for %+v in instance %s: %w",
			session, instance.ID, err)
	}
	return nil
}

// End marks the given session as ended
func (s *Service) End(ctx context.Context, instance *model.Instance, session *model.Session) apierror.Error {
	session.Status = constants.SESSEnded
	cdsSession := client_data.NewSessionFromSessionModel(session)
	if err := s.clientDataService.UpdateSessionStatus(ctx, cdsSession); err != nil {
		return apierror.Unexpected(err)
	}
	cdsSession.CopyToSessionModel(session)

	err := s.eventService.SessionEnded(ctx, s.db, instance, serialize.SessionToServerAPI(s.clock, session))
	if err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

// Remove marks the given session as removed
func (s *Service) Remove(ctx context.Context, instance *model.Instance, session *model.Session) apierror.Error {
	cdsSession := client_data.NewSessionFromSessionModel(session)
	cdsSession.Status = constants.SESSRemoved
	err := s.clientDataService.UpdateSessionStatus(ctx, cdsSession)
	if err != nil {
		return apierror.Unexpected(err)
	}
	cdsSession.CopyToSessionModel(session)

	err = s.eventService.SessionRemoved(ctx, s.db, instance, serialize.SessionToServerAPI(s.clock, session))
	if err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

// Revoke marks the given session as revoked
func (s *Service) Revoke(ctx context.Context, instance *model.Instance, session *model.Session) apierror.Error {
	cdsSession := client_data.NewSessionFromSessionModel(session)
	cdsSession.Status = constants.SESSRevoked
	err := s.clientDataService.UpdateSessionStatus(ctx, cdsSession)
	if err != nil {
		return apierror.Unexpected(err)
	}
	cdsSession.CopyToSessionModel(session)

	err = s.eventService.SessionRevoked(ctx, s.db, instance, serialize.SessionToServerAPI(s.clock, session))
	if err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

func (s *Service) RemoveAllWithoutClientTouch(ctx context.Context, sessions []*model.Session) error {
	for _, session := range sessions {
		session.Status = constants.SESSRemoved
		cdsSession := client_data.NewSessionFromSessionModel(session)
		if err := s.clientDataService.UpdateSessionStatusWithoutClientTouch(ctx, cdsSession); err != nil {
			return fmt.Errorf("RemoveAllWithoutClientTouch: updating status of %s to %s failed: %w", session.ID, session.Status, err)
		}
		cdsSession.CopyToSessionModel(session)
	}
	return nil
}

// RevokeAllForUserID marks all active sessions as revoked for a user with userID.
// No event is triggered.
func (s *Service) RevokeAllForUserID(ctx context.Context, instanceID, userID string) error {
	activeUserSessions, err := s.clientDataService.FindAllUserSessions(ctx, instanceID, userID, client_data.SessionFilterActiveOnly())
	if err != nil {
		return err
	}
	for _, session := range activeUserSessions {
		session.Status = constants.SESSRevoked
		if err := s.clientDataService.UpdateSessionStatus(ctx, session); err != nil {
			return err
		}
	}
	return nil
}

type TouchParams struct {
	Session              *model.Session
	EventSent            bool
	ActiveOrganizationID *null.String
	Activity             *model.SessionActivity
}

// Touch marks the given session as touched
func (s *Service) Touch(ctx context.Context, params TouchParams) error {
	cdsSession := client_data.NewSessionFromSessionModel(params.Session)
	cdsSession.TouchedAt = s.clock.Now().UTC()
	updatedColumns := []string{client_data.SessionColumns.TouchedAt}

	if params.EventSent {
		cdsSession.TouchEventSentAt = null.TimeFrom(params.Session.TouchedAt)
		updatedColumns = append(updatedColumns, client_data.SessionColumns.TouchEventSentAt)
	}
	if params.ActiveOrganizationID != nil {
		cdsSession.ActiveOrganizationID = *params.ActiveOrganizationID
		updatedColumns = append(updatedColumns, client_data.SessionColumns.ActiveOrganizationID)
	}

	err := s.clientDataService.UpdateSession(ctx, params.Session.InstanceID, params.Session.ClientID, cdsSession, updatedColumns...)
	if err != nil {
		return err
	}
	cdsSession.CopyToSessionModel(params.Session)

	if params.Activity == nil {
		// no activity given so we can return
		return nil
	}

	if !params.Session.SessionActivityID.Valid {
		// no session activity registered, create a new one
		params.Activity.SessionID = null.StringFrom(params.Session.ID)
		err := s.sessionActivitiesService.CreateSessionActivity(ctx, s.db, params.Session.InstanceID, params.Activity)
		if err != nil {
			return err
		}

		sessionActivityID := params.Activity.ID

		// update session with the new activity id
		cdsSession.SessionActivityID = null.StringFrom(sessionActivityID)
		err = s.clientDataService.UpdateSession(ctx, params.Session.InstanceID, params.Session.ClientID,
			cdsSession, client_data.SessionColumns.SessionActivityID)
		if err != nil {
			return err
		}
		cdsSession.CopyToSessionModel(params.Session)
		return nil
	}

	if !maintenance.FromContext(ctx) {
		// there is a session activity already associated with this session, so we need to update it
		params.Activity.ID = params.Session.SessionActivityID.String
		params.Activity.SessionID = null.StringFrom(params.Session.ID)
		if err := s.sessionActivitiesRepo.Update(ctx, s.db, params.Activity); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ConvertToSessionWithUser(ctx context.Context, instance *model.Instance, userSettings *usersettings.UserSettings, session *model.Session, authConfig *model.AuthConfig) (*model.SessionWithUser, apierror.Error) {
	sessionWithUser := model.SessionWithUser{
		Session:                 session,
		OrganizationMemberships: make([]*model.OrganizationMembershipSerializable, 0),
	}

	user, err := s.userRepo.QueryByID(ctx, s.db, session.UserID)
	if err != nil {
		return nil, apierror.Unexpected(
			fmt.Errorf("convertToSessionWithUser: retrieving session's %s user %s: %w", session.ID, session.UserID, err),
		)
	}
	if user == nil {
		return nil, nil
	}

	// make sure the user has a billing subscription, if the feature is enabled and does not have one
	if instance.HasBillingEnabledForUsers() && !user.BillingSubscriptionID.Valid {
		if apiErr := s.billingService.InitSubscriptionForUser(ctx, user); apiErr != nil {
			log.Error(ctx, "convertToSessionWithUser: initializing billing subscription for user %s: %v", user.ID, apiErr)
		}
	}

	// make sure the user has a subscription for the organization, if the feature is enabled and does not have one
	if instance.HasBillingEnabledForOrganizations() && session.ActiveOrganizationID.Valid {
		if apiErr := s.billingService.EnsureSubscriptionForOrganization(ctx, session.ActiveOrganizationID.String); apiErr != nil {
			log.Error(ctx, "convertToSessionWithUser: initializing organization memberships for user %s: %v", user.ID, apiErr)
		}
	}

	sessionWithUser.User, err = s.serializableService.ConvertUser(ctx, s.db, userSettings, user)
	if err != nil {
		return nil, apierror.Unexpected(
			fmt.Errorf("convertToSessionWithUser: serializing user %+v: %w", user, err),
		)
	}

	var identifierID string
	identifierID, err = s.signInIdentificationID(ctx, session.ID)
	if err != nil {
		return nil, apierror.Unexpected(
			fmt.Errorf("convertToSessionWithUser: retrieving identifier id from sign in with created session id %s: %w", session.ID, err),
		)
	}

	if identifierID == "" {
		identifierID, err = s.signUpIdentificationID(ctx, session.ID)
		if err != nil {
			return nil, apierror.Unexpected(
				fmt.Errorf("convertToSessionWithUser: retrieving identifier id from sign up with created session id %s: %w", session.ID, err),
			)
		}
	}

	if identifierID != "" {
		identification, err := s.identificationRepo.QueryByID(ctx, s.db, identifierID)
		if err != nil {
			return nil, apierror.Unexpected(
				fmt.Errorf("convertToSessionWithUser: retrieving identification with id %s: %w", identifierID, err),
			)
		}

		if identification == nil {
			// identification of current session does not exist (maybe deleted), use user's alternative identification (primary or username)
			identification, err = s.fetchUserAlternativeIdentification(ctx, sessionWithUser.User.User)
			if err != nil {
				return nil, apierror.Unexpected(
					fmt.Errorf("convertToSessionWithUser: retrieving alternative identification of user %s: %w", sessionWithUser.User.ID, err),
				)
			}
		}
		// Cannot find any identification for this user. Probably the whole user has been deleted at this point.
		// Fail fast.
		if identification == nil {
			return nil, apierror.IdentificationNotFound(identifierID)
		}

		if identification.TargetIdentificationID.Valid {
			// this is an OAuth identification, so we need to find the connected identification which
			// contains the actual identifier
			targetIdentificationID := identification.TargetIdentificationID.String
			identification, err = s.identificationRepo.QueryByID(ctx, s.db, targetIdentificationID)
			if identification == nil {
				return nil, apierror.IdentificationNotFound(targetIdentificationID)
			}
			if err != nil {
				return nil, apierror.Unexpected(
					fmt.Errorf("convertToSessionWithUser: retrieving target identification with id %s: %w", targetIdentificationID, err),
				)
			}
		}

		sessionWithUser.Identifier = identification.Identifier.String
	}

	// We only want to include the user's memberships if organization feature is on
	if authConfig.IsOrganizationsEnabled() {
		memberships, err := s.orgMembershipRepo.FindAllByUser(ctx, s.db, sessionWithUser.User.ID)
		if err != nil {
			return nil, apierror.Unexpected(fmt.Errorf("convertToSessionWithUser: retrieving org memberships for user %s: %w", sessionWithUser.User.ID, err))
		}

		serializableMemberships := make([]*model.OrganizationMembershipSerializable, len(memberships))
		for i, membership := range memberships {
			serializableMemberships[i], err = s.orgService.ConvertToSerializable(ctx, s.db, membership)
			if err != nil {
				return nil, apierror.Unexpected(fmt.Errorf("convertToSessionWithUser: converting membership %s to serializable: %w", membership.ID, err))
			}
		}
		sessionWithUser.OrganizationMemberships = serializableMemberships
	}

	return &sessionWithUser, nil
}

func (s *Service) fetchUserAlternativeIdentification(ctx context.Context, user *model.User) (*model.Identification, error) {
	var identificationID string
	if user.PrimaryEmailAddressID.Valid {
		identificationID = user.PrimaryEmailAddressID.String
	} else if user.PrimaryPhoneNumberID.Valid {
		identificationID = user.PrimaryPhoneNumberID.String
	} else if user.PrimaryWeb3WalletID.Valid {
		identificationID = user.PrimaryWeb3WalletID.String
	}
	if identificationID != "" {
		return s.identificationRepo.QueryByID(ctx, s.db, identificationID)
	}
	return s.identificationRepo.QueryByTypeAndUser(ctx, s.db, constants.ITUsername, user.ID)
}

func (s *Service) signInIdentificationID(ctx context.Context, sessionID string) (string, error) {
	signIn, err := s.signInRepo.QueryByCreatedSessionID(ctx, s.db, sessionID)
	if err != nil {
		return "", fmt.Errorf("signInIdentificationID: retrieving sign in with created session id %s: %w",
			sessionID, err)
	}
	if signIn == nil {
		return "", nil
	}
	return signIn.IdentificationID.String, nil
}

func (s *Service) signUpIdentificationID(ctx context.Context, sessionID string) (string, error) {
	signUp, err := s.signUpRepo.QueryByCreatedSessionID(ctx, s.db, sessionID)
	if err != nil {
		return "", fmt.Errorf("signUpIdentificationID: retrieving sign up with created session id %s: %w",
			sessionID, err)
	}
	if signUp == nil {
		return "", nil
	}

	if signUp.EmailAddressID.Valid {
		return signUp.EmailAddressID.String, nil
	} else if signUp.PhoneNumberID.Valid {
		return signUp.PhoneNumberID.String, nil
	}

	// if no email or phone number, try the external account
	return signUp.SuccessfulExternalAccountIdentificationID.String, nil
}

func determineProvider(externalAccount *model.ExternalAccount) string {
	if externalAccount == nil {
		return "clerk"
	}

	return externalAccount.Provider
}

func (s *Service) DeleteUserSessions(ctx context.Context, instanceID, userID string) error {
	sessions, err := s.clientDataService.FindAllUserSessions(ctx, instanceID, userID, nil)
	if err != nil {
		return err
	}

	for _, session := range sessions {
		if err := s.clientDataService.DeleteSession(ctx, session.InstanceID, session.ClientID, session.ID); err != nil {
			return err
		}
	}
	return nil
}
