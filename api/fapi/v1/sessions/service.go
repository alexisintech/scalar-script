package sessions

import (
	"context"
	"errors"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	sharedcookies "clerk/api/shared/cookies"
	"clerk/api/shared/events"
	"clerk/api/shared/features"
	"clerk/api/shared/sessions"
	"clerk/model"
	clerkbilling "clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/requesting_session"
	"clerk/pkg/ctx/requesting_user"
	"clerk/pkg/ctxkeys"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/set"
	clerktime "clerk/pkg/time"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock clockwork.Clock
	db    database.Database

	// services
	eventService        *events.Service
	featureService      *features.Service
	sessionService      *sessions.Service
	sharedCookieService *sharedcookies.Service

	// repositories
	organizationRepo           *repository.Organization
	organizationMembershipRepo *repository.OrganizationMembership
	subscriptionPlanRepo       *repository.SubscriptionPlans
	userRepo                   *repository.Users
	sessionActivityRepo        *repository.SessionActivities

	clientDataService *client_data.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:                      deps.Clock(),
		db:                         deps.DB(),
		eventService:               events.NewService(deps),
		featureService:             features.NewService(deps.DB(), deps.GueClient()),
		sessionService:             sessions.NewService(deps),
		organizationRepo:           repository.NewOrganization(),
		organizationMembershipRepo: repository.NewOrganizationMembership(),
		subscriptionPlanRepo:       repository.NewSubscriptionPlans(),
		userRepo:                   repository.NewUsers(),
		sessionActivityRepo:        repository.NewSessionActivities(),
		clientDataService:          client_data.NewService(deps),
		sharedCookieService:        sharedcookies.NewService(deps),
	}
}

// Read returns the active session that is in the context, wrapped with the current client
func (s *Service) Read(ctx context.Context, sessionID string) (*serialize.SessionClientResponse, apierror.Error) {
	session, sessionErr := s.loadSessionFromCtx(ctx, sessionID)
	if sessionErr != nil {
		return nil, sessionErr
	}

	// TODO: Do we need this check or should we read all sessions?
	if session.IsAbandoned(s.clock) {
		return nil, apierror.SessionNotFound(sessionID)
	}

	return s.toResponse(ctx, session)
}

type TouchParams struct {
	SessionID            string
	ActiveOrganizationID *null.String
	Activity             *model.SessionActivity
}

// Touch marks the given session as touch on the given time
func (s *Service) Touch(ctx context.Context, params TouchParams) (*serialize.SessionClientResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	session, err := s.loadSessionFromCtx(ctx, params.SessionID)
	if err != nil {
		return nil, err
	}

	if session.GetStatus(s.clock) != constants.SESSActive {
		return nil, apierror.SignedOut()
	}

	if maxRate := cenv.GetDurationInSeconds(cenv.ClerkMaxSessionTouchRateSeconds); maxRate > 0 {
		// Rate Limiting window check (maxRate = 1s):
		// session.TouchedAt      -> 12:34:56.789
		// session.TouchedAt + 1" -> 12:34:57.789
		// clock.Now              -> 12:34:56.990 -> Rate Limited
		withinRateLimitWindow := s.clock.Now().Before(session.TouchedAt.Add(maxRate))
		if !shouldChange(session, params) && withinRateLimitWindow {
			return s.toResponse(ctx, session)
		}
	}

	if params.ActiveOrganizationID != nil && params.ActiveOrganizationID.Valid {
		// ensure organization exists
		orgExists, err := s.organizationRepo.ExistsByIDAndInstance(ctx, s.db, params.ActiveOrganizationID.String, env.Instance.ID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		if !orgExists {
			return nil, apierror.OrganizationNotFound()
		}

		// ensure user is a member of that organization
		isMember, err := s.organizationMembershipRepo.ExistsByOrganizationAndUser(ctx, s.db, params.ActiveOrganizationID.String, session.UserID)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		if !isMember {
			return nil, apierror.NotAMemberInOrganization()
		}
	}

	// If the previous Session record state contains a last event sent timestamp
	// after the start of the day, we have already sent an event for this session
	// and consequently for this user. Note that this is an optimization technique,
	// in order to avoid querying the `events` table too frequently,
	// since we only write the event once per day.
	var eventSent bool
	if session.TouchEventSentAt.Time.Before(clerktime.StartOfDay(s.clock.Now().UTC())) {
		err := s.eventService.SessionTouched(ctx, s.db, env.Instance, session)
		if err != nil {
			// Not fatal if we fail saving/delivering an event, so we only log and
			// continue
			sentryclerk.CaptureException(ctx, err)
		} else if !maintenance.FromContext(ctx) {
			eventSent = true
		}
	}

	if err := s.sessionService.Touch(ctx, sessions.TouchParams{
		Session:              session,
		ActiveOrganizationID: params.ActiveOrganizationID,
		Activity:             params.Activity,
		EventSent:            eventSent,
	}); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return s.toResponse(ctx, session)
}

// shouldChange compares the Session instance state with the Touch parameters
// and determines whether an update should occur. If not, the request
// may be eligible for rate limiting.
func shouldChange(session *model.Session, params TouchParams) bool {
	// If params.ActiveOrganizationID is present and the value differs from
	// the Session record value, the session is eligible for update.
	if params.ActiveOrganizationID != nil && params.ActiveOrganizationID.String != session.ActiveOrganizationID.String {
		return true
	}
	// If an activity is not associated with the session,
	// we need to assign one
	if !session.SessionActivityID.Valid {
		return true
	}
	return false
}

// End marks session as ended
func (s *Service) End(ctx context.Context, client *model.Client, sessionID string) (*serialize.SessionClientResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	session, sessionErr := s.loadSessionFromCtx(ctx, sessionID)
	if sessionErr != nil {
		return nil, sessionErr
	}

	if !session.CanEnd(s.clock) {
		return nil, apierror.InvalidActionForSession(session.ID, "end")
	}

	err := s.sessionService.End(ctx, env.Instance, session)

	if err := s.sharedCookieService.UpdateClientCookieValue(ctx, env.Instance, client); err != nil {
		return nil, apierror.Unexpected(
			fmt.Errorf("session/end: updating client cookie for %s: %w", client.ID, err),
		)
	}

	if err != nil {
		return nil, err
	}

	return s.toResponse(ctx, session)
}

// Remove deletes the given session
func (s *Service) Remove(ctx context.Context, sessionID string) (*serialize.SessionClientResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	session, sessionErr := s.loadSessionFromCtx(ctx, sessionID)
	if sessionErr != nil {
		return nil, sessionErr
	}

	if !session.CanRemove(s.clock) {
		return nil, apierror.InvalidActionForSession(session.ID, "remove")
	}

	err := s.sessionService.Remove(ctx, env.Instance, session)
	if err != nil {
		return nil, err
	}

	return s.toResponse(ctx, session)
}

// Revoke marks the given session as revoked.
// A user can only revoke his own sessions and cannot revoke the active session that is used to make the request.
func (s *Service) Revoke(ctx context.Context, sessionID string) (*serialize.SessionClientResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	requestingSession := requesting_session.FromContext(ctx)
	requestingUser := requesting_user.FromContext(ctx)

	if sessionID == requestingSession.ID {
		return nil, apierror.InvalidActionForSession(sessionID, "revoke")
	}

	userSessions, err := s.clientDataService.FindAllUserSessions(ctx, env.Instance.ID, requestingUser.ID, nil)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var userSessionToRevoke *client_data.Session
	for _, userSession := range userSessions {
		if userSession.ID == sessionID {
			userSessionToRevoke = userSession
			break
		}
	}

	if userSessionToRevoke == nil {
		return nil, apierror.UnauthorizedActionForSession(sessionID)
	}
	sessionToRevoke := userSessionToRevoke.ToSessionModel()

	if !sessionToRevoke.CanRevoke(s.clock) {
		return nil, apierror.InvalidActionForSession(sessionToRevoke.ID, "revoke")
	}

	if apiErr := s.sessionService.Revoke(ctx, env.Instance, sessionToRevoke); apiErr != nil {
		return nil, apiErr
	}

	if err := s.userRepo.UpdateUpdatedAtByID(ctx, s.db, sessionToRevoke.UserID); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return s.toResponse(ctx, sessionToRevoke)
}

// ListUserSessions returns all sessions associated with the given user
func (s *Service) ListUserSessions(ctx context.Context, userID string) ([]*serialize.SessionClientResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	requestingSession := requesting_session.FromContext(ctx)

	sessionList, err := s.clientDataService.FindAllUserSessions(ctx, env.Instance.ID, userID, nil)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	userSessions := make([]*model.Session, len(sessionList))
	for i := range sessionList {
		userSessions[i] = sessionList[i].ToSessionModel()
	}

	responses := make([]*serialize.SessionClientResponse, 0)
	for _, session := range userSessions {
		if session.HasActor() && !requestingSession.HasActor() {
			// impersonation sessions are only included in the response if the
			// requesting session is also impersonation
			continue
		}
		resp, err := s.toResponse(ctx, session)
		if err != nil {
			return nil, err
		}
		responses = append(responses, resp)
	}

	return responses, nil
}

// ListUserActiveSessions returns all active sessions for the given user
func (s *Service) ListUserActiveSessions(ctx context.Context, userID string) ([]*serialize.SessionClientResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	requestingSession := requesting_session.FromContext(ctx)

	subscriptionPlans, err := s.subscriptionPlanRepo.FindAllBySubscription(ctx, s.db, env.Subscription.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	unsupportedFeatures := clerkbilling.ValidateSupportedFeatures(set.New(clerkbilling.Features.DeviceTracking), env.Subscription, subscriptionPlans...)
	deviceTrackingEnabled := env.Instance.HasAccessToAllFeatures() || len(unsupportedFeatures) == 0

	var userSessions []*model.Session
	if deviceTrackingEnabled {
		sessionList, err := s.clientDataService.FindAllUserSessions(ctx, env.Instance.ID, userID, client_data.SessionFilterActiveOnly())
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		for i := range sessionList {
			userSessions = append(userSessions, sessionList[i].ToSessionModel())
		}
	} else {
		// if device tracking is not supported by the application's subscription plan,
		// only return the current session
		userSessions = append(userSessions, requestingSession)
	}

	userSessionsWithActivity := make([]*model.SessionWithActivity, len(userSessions))
	for i, sess := range userSessions {
		userSessionsWithActivity[i] = &model.SessionWithActivity{Session: sess}
	}

	if deviceTrackingEnabled {
		sessionActivityIDs := make([]string, 0, len(userSessions))
		for _, sess := range userSessions {
			if !sess.SessionActivityID.Valid {
				continue
			}
			sessionActivityIDs = append(sessionActivityIDs, sess.SessionActivityID.String)
		}
		activities, err := s.sessionActivityRepo.FindAllByIDs(ctx, s.db, sessionActivityIDs)
		if err != nil {
			return nil, apierror.Unexpected(err)
		}
		for _, sessionWithActivity := range userSessionsWithActivity {
			for _, activity := range activities {
				if activity.ID == sessionWithActivity.Session.SessionActivityID.String {
					sessionWithActivity.LatestActivity = activity
					break
				}
			}
		}
	}

	responses := make([]*serialize.SessionClientResponse, 0)
	for _, session := range userSessionsWithActivity {
		if session.HasActor() && !requestingSession.HasActor() {
			// impersonation sessions are only included in the response if the
			// requesting session is also an impersonation session
			continue
		}
		responses = append(responses, serialize.SessionWithActivityToClientAPI(s.clock, session))
	}

	return responses, nil
}

func (s *Service) toResponse(ctx context.Context, session *model.Session) (*serialize.SessionClientResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	sessionWithUser, apiErr := s.sessionService.ConvertToSessionWithUser(ctx, env.Instance, userSettings, session, env.AuthConfig)
	if apiErr != nil {
		return nil, apiErr
	}
	if sessionWithUser == nil {
		return nil, apierror.SessionNotFound(session.ID)
	}

	response, err := serialize.SessionToClientAPI(ctx, s.clock, sessionWithUser)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	return response, nil
}

// GetCurrentClientSession returns a session and ensures that it belongs to the requesting client
func (s *Service) GetCurrentClientSession(ctx context.Context, sessionID string) (*model.Session, apierror.Error) {
	env := environment.FromContext(ctx)
	client := ctx.Value(ctxkeys.RequestingClient).(*model.Client)

	session, err := s.clientDataService.FindSession(ctx, env.Instance.ID, client.ID, sessionID)
	if err != nil {
		if errors.Is(err, client_data.ErrNoRecords) {
			return nil, apierror.SessionNotFound(sessionID)
		}
		return nil, apierror.Unexpected(err)
	}

	return session.ToSessionModel(), nil
}

// loadSessionFromCtx fetches current session from the request context.
// If session is missing, an API not found error is returned.
func (s *Service) loadSessionFromCtx(ctx context.Context, sessionID string) (*model.Session, apierror.Error) {
	session := requesting_session.FromContext(ctx)
	if session == nil {
		return nil, apierror.SessionNotFound(sessionID)
	}
	if session.ID != sessionID {
		return nil, apierror.SessionNotFound(sessionID)
	}
	return session, nil
}
