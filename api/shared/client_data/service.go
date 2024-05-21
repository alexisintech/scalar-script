package client_data

import (
	"context"
	"errors"

	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/utils/clerk"

	"github.com/jonboulle/clockwork"
)

type SessionFilterParams struct {
	ActiveOnly bool
}

func (s *SessionFilterParams) activeOnly() bool {
	if s == nil {
		return false
	}
	return s.ActiveOnly
}

func SessionFilterActiveOnly() *SessionFilterParams {
	return &SessionFilterParams{ActiveOnly: true}
}

func defaultSessionFilterParams() *SessionFilterParams {
	return &SessionFilterParams{
		ActiveOnly: false,
	}
}

// DataStore is the generic interface to manage Client & Session data
type DataStore interface {
	// Clients
	CreateClient(ctx context.Context, instanceID string, client *Client) error
	FindClient(ctx context.Context, instanceID, clientID string) (*Client, error)
	UpdateClient(ctx context.Context, instanceID string, client *Client, cols ...string) error
	DeleteClient(ctx context.Context, instanceID, clientID string) error
	// FindClientByIDAndRotatingToken & FindClientByIDAndRotatingTokenNonce are security-sensitive methods that
	// likely need to be reimplemented in the FAPI Session API worker, that'll serve certain session/client-related
	// FAPI endpoints directly. In order to ease the transition, we're defining them at the datastore level now and
	// we can replace them afterwards when we re-evaluate the datastore layer surface.
	FindClientByIDAndRotatingToken(ctx context.Context, instanceID, clientID, rotatingToken string) (*Client, error)
	FindClientByIDAndRotatingTokenNonce(ctx context.Context, instanceID, clientID, rotatingTokenNonce string) (*Client, error)

	// Sessions
	FindUserSession(ctx context.Context, instanceID, userID, sessionID string) (*Session, error)
	CreateSession(ctx context.Context, instanceID, clientID string, session *Session) error
	FindSession(ctx context.Context, instanceID, clientID, sessionID string) (*Session, error)
	UpdateSession(ctx context.Context, instanceID, clientID string, session *Session, cols ...string) error
	DeleteSession(ctx context.Context, instanceID, clientID, sessionID string) error

	// Sessions (Bulk Fetch)
	FindAllUserSessions(ctx context.Context, instanceID, userID string, filterParams *SessionFilterParams) ([]*Session, error)
	FindAllClientSessions(ctx context.Context, instanceID, clientID string, filterParams *SessionFilterParams) ([]*Session, error)
	FindAllClientsSessions(ctx context.Context, instanceID string, clientIDs []string, filterParams *SessionFilterParams) ([]*Session, error)
}

// This Service is responsible for providing data access to Client / Session data.
// This is provided a DataStore, which then enables the Service to read / write
// Clients & Sessions to the specific data store implementation.
//
// This also provides higher-level functionality, which is performed via
// interfacing with the provided DataStore.
//
// Initially, we will build out a PostgreSQL interface, but eventually
// we'll implement a Durable Object interface once our Data is migrated.
//
// This service will keep the remainder of the codebase agnostic to any
// specific data store is managing this data. Once we are migrated to
// Durable Objects (or a different data store), we should be able to swap
// out to our provided DataStore with little to no code changes elsewhere.
type Service struct {
	dataStore
	clock    clockwork.Clock
	cascader *DeleteCascader
}

// dataStore is an unexported alias of the DataStore interface
// used for embedding in the Service struct and automatically provide
// access to the interface methods without allowing reassignment, since
// the field is unexported.
type dataStore DataStore

func NewService(deps clerk.Deps) *Service {
	return &Service{
		dataStore: newTransitionDatastore(deps),
		clock:     deps.Clock(),
		cascader:  NewDeleteCascader(deps),
	}
}

func (s *Service) TouchClient(ctx context.Context, instanceID, clientID string) error {
	client := &Client{ID: clientID}
	return s.UpdateClient(ctx, instanceID, client, ClientColumns.UpdatedAt)
}

func (s *Service) UpdateClientSignInID(ctx context.Context, instanceID string, client *Client) error {
	return s.UpdateClient(ctx, instanceID, client, ClientColumns.SignInID)
}

func (s *Service) UpdateClientSignUpID(ctx context.Context, instanceID string, client *Client) error {
	return s.UpdateClient(ctx, instanceID, client, ClientColumns.SignUpID)
}

func (s *Service) UpdateClientPostponeCookieUpdateAndSignUpID(ctx context.Context, instanceID string, client *Client) error {
	return s.UpdateClient(ctx, instanceID, client, ClientColumns.PostponeCookieUpdate, ClientColumns.SignUpID)
}

func (s *Service) UpdateClientPostponeCookieUpdateAndSignInID(ctx context.Context, instanceID string, client *Client) error {
	return s.UpdateClient(ctx, instanceID, client, ClientColumns.PostponeCookieUpdate, ClientColumns.SignInID)
}

func (s *Service) UpdateClientPostponeCookieUpdate(ctx context.Context, instanceID string, client *Client) error {
	return s.UpdateClient(ctx, instanceID, client, ClientColumns.PostponeCookieUpdate)
}

func (s *Service) UpdateClientRotatingTokenNonce(ctx context.Context, instanceID string, client *Client) error {
	return s.UpdateClient(ctx, instanceID, client, ClientColumns.RotatingTokenNonce)
}

func (s *Service) UpdateClientCookieValue(ctx context.Context, instanceID string, client *Client) error {
	return s.UpdateClient(ctx, instanceID, client, ClientColumns.CookieValue)
}

// UpdateSessionStatus updates the `Status` column of a session.
// This also Touches the Sessions associated Client.
func (s *Service) UpdateSessionStatus(ctx context.Context, session *Session) error {
	err := s.TouchClient(ctx, session.InstanceID, session.ClientID)
	if err != nil {
		return err
	}
	return s.UpdateSession(ctx, session.InstanceID, session.ClientID, session, SessionColumns.Status)
}

// UpdateSessionStatusWithoutClientTouch updates the `Status` column of a session.
func (s *Service) UpdateSessionStatusWithoutClientTouch(ctx context.Context, session *Session) error {
	return s.UpdateSession(ctx, session.InstanceID, session.ClientID, session, SessionColumns.Status)
}

func (s *Service) UpdateSessionTokenIssuedAt(ctx context.Context, session *Session) error {
	return s.UpdateSession(ctx, session.InstanceID, session.ClientID, session, SessionColumns.TokenIssuedAt)
}

func (s *Service) UpdateSessionTokenIssuedAtAndTokenCreatedEventSentAt(ctx context.Context, session *Session) error {
	return s.UpdateSession(ctx, session.InstanceID, session.ClientID, session, SessionColumns.TokenIssuedAt, SessionColumns.TokenCreatedEventSentAt)
}

func (s *Service) UpdateSessionActiveOrganizationID(ctx context.Context, session *Session) error {
	return s.UpdateSession(ctx, session.InstanceID, session.ClientID, session, SessionColumns.ActiveOrganizationID)
}

func (s *Service) UpdateSessionSessionActivityID(ctx context.Context, session *Session) error {
	return s.UpdateSession(ctx, session.InstanceID, session.ClientID, session, SessionColumns.SessionActivityID)
}

func (s *Service) UpdateSessionReplacementSessionID(ctx context.Context, session *Session) error {
	return s.UpdateSession(ctx, session.InstanceID, session.ClientID, session, SessionColumns.ReplacementSessionID)
}

func (s *Service) UpdateClientToSignInAccountTransferID(ctx context.Context, client *Client) error {
	return s.UpdateClient(ctx, client.InstanceID, client, ClientColumns.ToSignInAccountTransferID)
}

func (s *Service) UpdateClientToSignUpAccountTransferID(ctx context.Context, client *Client) error {
	return s.UpdateClient(ctx, client.InstanceID, client, ClientColumns.ToSignUpAccountTransferID)
}

func (s *Service) QueryClient(ctx context.Context, instanceID, clientID string) (*Client, error) {
	// First we fetch the Client to see if that exists
	client, err := s.FindClient(ctx, instanceID, clientID)
	if errors.Is(err, ErrNoRecords) {
		return nil, nil
	} else if err != nil {
		return nil, clerkerrors.WithStacktrace(
			"client_data.Service: QueryClient (instance=%s client=%s): %w",
			instanceID, clientID, err)
	}
	return client, nil
}

// // QueryLatestTouchedActiveSessionByClient returns the last touched active session for the given client
func (s *Service) QueryLatestTouchedActiveSessionByClient(ctx context.Context, instanceID, clientID string) (*Session, error) {
	// Fetch all of the Active Sessions for this client
	activeSessions, err := s.FindAllClientSessions(ctx, instanceID, clientID, &SessionFilterParams{
		ActiveOnly: true,
	})
	if err != nil {
		return nil, clerkerrors.WithStacktrace(
			"client_data.Service: QueryLatestTouchedActiveSessionByClient (instance=%s client=%s): %w",
			instanceID, clientID, err)
	}

	// Find the most recently touched one
	var lastTouchedSession *Session
	for _, cSession := range activeSessions {
		if lastTouchedSession == nil || lastTouchedSession.TouchedAt.Before(cSession.TouchedAt) {
			lastTouchedSession = cSession
		}
	}
	return lastTouchedSession, nil
}

// // FindAllCurrentSessionsByClients includes all sessions for the given client, some may be in a signed_out state
func (s *Service) FindAllCurrentSessionsByClients(ctx context.Context, instanceID string, clientIDs []string) ([]*Session, error) {
	// Fetch all of the Clients Sessions
	clientSessions, err := s.FindAllClientsSessions(ctx, instanceID, clientIDs, nil)
	if err != nil {
		return nil, clerkerrors.WithStacktrace(
			"client_data.Service: FindAllCurrentSessionsByClients (instance=%s client_ids=%v): %w",
			instanceID, clientIDs, err)
	}

	// Filter down the sessions to the "Current" sessions. These slightly differ from Active Sessions.
	var currentSessions []*Session
	for _, clientSession := range clientSessions {
		if !clientSession.ReplacementSessionID.Valid &&
			clientSession.Status != constants.SESSRemoved &&
			clientSession.Status != constants.SESSPendingActivation &&
			clientSession.AbandonAt.After(s.clock.Now()) {
			currentSessions = append(currentSessions, clientSession)
		}
	}

	return currentSessions, nil
}

// FindAllCurrentSessionsByClientsWithoutExpiredSessions retrieves all active sessions for the given clients, excluding expired sessions.
// However, if there are no active sessions, it includes the most recently expired session.
// Some sessions may be in a signed_out state.
func (s *Service) FindAllCurrentSessionsByClientsWithoutExpiredSessions(ctx context.Context, instanceID string, clientIDs []string) ([]*Session, error) {
	sessions, err := s.FindAllCurrentSessionsByClients(ctx, instanceID, clientIDs)

	if err != nil {
		return nil, err
	}

	var result []*Session
	var mostRecentlyExpiredSession *Session
	for _, session := range sessions {
		if session.ToSessionModel().GetStatus(s.clock) == constants.SESSExpired {
			if mostRecentlyExpiredSession == nil || session.ExpireAt.After(mostRecentlyExpiredSession.ExpireAt) {
				mostRecentlyExpiredSession = session
			}
			continue
		}
		result = append(result, session)
	}

	if len(result) == 0 && mostRecentlyExpiredSession != nil {
		result = append(result, mostRecentlyExpiredSession)
	}

	return result, nil
}

func (s *Service) QuerySessionsActiveByInstanceAndClientAndUser(ctx context.Context, instanceID, clientID, userID string) (*Session, error) {
	activeSessions, err := s.FindAllClientSessions(ctx, instanceID, clientID, &SessionFilterParams{ActiveOnly: true})
	if err != nil {
		return nil, clerkerrors.WithStacktrace(
			"client_data.Service: QueryClient (instance=%s client=%s user=%s): %w",
			instanceID, clientID, userID, err)
	}
	if len(activeSessions) == 0 {
		return nil, nil
	}
	for _, session := range activeSessions {
		if session.UserID == userID {
			return session, nil
		}
	}
	return nil, nil
}

func (s *Service) QuerySessionsLatestTouchedByUser(ctx context.Context, instanceID, userID string) (*Session, error) {
	userSessions, err := s.FindAllUserSessions(ctx, instanceID, userID, nil)
	if err != nil {
		return nil, clerkerrors.WithStacktrace(
			"client_data.Service: QuerySessionsByUserLatestTouchedAt (instance=%s user=%s): %w",
			instanceID, userID, err)
	}

	// Find the most recently touched one
	var latestTouchedSession *Session
	for _, cSession := range userSessions {
		if latestTouchedSession == nil || cSession.TouchedAt.After(latestTouchedSession.TouchedAt) {
			latestTouchedSession = cSession
		}
	}
	return latestTouchedSession, nil
}

func (s *Service) DeleteSession(ctx context.Context, instanceID, clientID, sessionID string) error {
	if err := s.dataStore.DeleteSession(ctx, instanceID, clientID, sessionID); err != nil {
		return err
	}
	return s.cascader.OnSessionDeleted(ctx, instanceID, clientID, sessionID)
}

func (s *Service) DeleteClient(ctx context.Context, instanceID, clientID string) error {
	if err := s.dataStore.DeleteClient(ctx, instanceID, clientID); err != nil {
		return err
	}
	return s.cascader.OnClientDeleted(ctx, instanceID, clientID)
}
