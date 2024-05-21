package client_data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/cache"
	"clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/recovery"
	sentryclerk "clerk/pkg/sentry"

	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
)

type postgresDataStore struct {
	db          database.Database
	cache       cache.Cache
	clientRepo  *repository.Clients
	sessionRepo *repository.Sessions
}

func newPostgresDatastore(deps clerk.Deps) DataStore {
	return &postgresDataStore{
		db:          deps.DB(),
		cache:       deps.Cache(),
		clientRepo:  repository.NewClients(),
		sessionRepo: repository.NewSessions(deps.Clock()),
	}
}

func (s *postgresDataStore) CreateClient(ctx context.Context, instanceID string, client *Client) error {
	postgresClient := &model.Client{Client: &sqbmodel.Client{}}
	client.CopyToClientModel(postgresClient)
	postgresClient.InstanceID = instanceID
	err := s.clientRepo.Insert(ctx, s.db, postgresClient)
	if err != nil {
		return err
	}
	client.CopyFromClientModel(postgresClient)
	return nil
}

func (s *postgresDataStore) FindClient(ctx context.Context, instanceID, clientID string) (*Client, error) {
	postgresClient, err := s.findClient(ctx, clientID, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, clerkerrors.WithStacktrace("%s: %w", err.Error(), ErrNoRecords)
	} else if err != nil {
		return nil, err
	} else if postgresClient.InstanceID != instanceID {
		return nil, ErrNoRecords
	}

	client := &Client{}
	client.CopyFromClientModel(postgresClient)
	return client, nil
}

func (s *postgresDataStore) FindClientByIDAndRotatingToken(ctx context.Context, instanceID, clientID, rotatingToken string) (*Client, error) {
	postgresClient, err := s.findClient(ctx, clientID, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, clerkerrors.WithStacktrace("%s: %w", err.Error(), ErrNoRecords)
	} else if err != nil {
		return nil, err
	} else if postgresClient.InstanceID != instanceID || postgresClient.RotatingToken != rotatingToken {
		return nil, ErrNoRecords
	}
	return NewClientFromClientModel(postgresClient), nil
}

func (s *postgresDataStore) FindClientByIDAndRotatingTokenNonce(ctx context.Context, instanceID, clientID, rotatingTokenNonce string) (*Client, error) {
	postgresClient, err := s.findClient(ctx, clientID, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, clerkerrors.WithStacktrace("%s: %w", err.Error(), ErrNoRecords)
	} else if err != nil {
		return nil, err
	} else if postgresClient.InstanceID != instanceID ||
		!postgresClient.RotatingTokenNonce.Valid ||
		postgresClient.RotatingTokenNonce.String != rotatingTokenNonce {
		return nil, ErrNoRecords
	}
	return NewClientFromClientModel(postgresClient), nil
}

func (s *postgresDataStore) UpdateClient(ctx context.Context, instanceID string, client *Client, cols ...string) error {
	postgresClient := &model.Client{Client: &sqbmodel.Client{}}
	client.CopyToClientModel(postgresClient)
	if maintenance.FromContext(ctx) {
		if err := s.cache.Set(ctx, maintenanceClientKey(postgresClient.ID, instanceID), postgresClient, time.Hour); err != nil {
			return err
		}
	} else {
		if err := s.clientRepo.Update(ctx, s.db, postgresClient, cols...); err != nil {
			return err
		}
	}

	client.CopyFromClientModel(postgresClient)
	return nil
}

func (s *postgresDataStore) DeleteClient(ctx context.Context, instanceID, clientID string) error {
	// Keeping a Transaction here to be able to handle cascading logic later
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.clientRepo.DeleteByInstanceAndID(ctx, tx, instanceID, clientID)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	return txErr
}

func (s *postgresDataStore) FindAllUserSessions(ctx context.Context, instanceID, userID string, filterParams *SessionFilterParams) ([]*Session, error) {
	// Set the defaults for the filter params if not set
	if filterParams == nil {
		filterParams = defaultSessionFilterParams()
	}

	// Fetch Sessions
	var err error
	var postgresSessions []*model.Session
	if !filterParams.ActiveOnly {
		postgresSessions, err = s.sessionRepo.FindAllByInstanceAndUser(ctx, s.db, instanceID, userID)
	} else {
		postgresSessions, err = s.sessionRepo.FindAllActiveSessionsByInstanceAndUser(ctx, s.db, instanceID, userID)
	}
	if err != nil {
		return nil, err
	}

	postgresSessions = s.loadMaintenanceUpdatesIfNecessary(ctx, postgresSessions)

	// Copy over the Postgres Sessions over to a []*Session array
	resultSessions := make([]*Session, len(postgresSessions))
	for i, pgSession := range postgresSessions {
		newSession := &Session{}
		newSession.CopyFromSessionModel(pgSession)
		resultSessions[i] = newSession
	}
	return resultSessions, nil
}

func (s *postgresDataStore) FindUserSession(ctx context.Context, instanceID, userID, sessionID string) (*Session, error) {
	postgresSession, err := s.findSession(ctx, sessionID, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, clerkerrors.WithStacktrace("%s: %w", err.Error(), ErrNoRecords)
	} else if err != nil {
		return nil, err
	}
	if postgresSession.InstanceID != instanceID || postgresSession.UserID != userID {
		return nil, ErrNoRecords
	}
	session := &Session{}
	session.CopyFromSessionModel(postgresSession)
	return session, nil
}

func (s *postgresDataStore) FindAllClientSessions(ctx context.Context, instanceID, clientID string, filterParams *SessionFilterParams) ([]*Session, error) {
	return s.FindAllClientsSessions(ctx, instanceID, []string{clientID}, filterParams)
}

func (s *postgresDataStore) FindAllClientsSessions(ctx context.Context, instanceID string, clientIDs []string, filterParams *SessionFilterParams) ([]*Session, error) {
	// Set the defaults for the filter params if not set
	if filterParams == nil {
		filterParams = defaultSessionFilterParams()
	}

	// Fetch Sessions
	var err error
	var postgresSessions []*model.Session
	if !filterParams.ActiveOnly {
		postgresSessions, err = s.sessionRepo.FindAllByInstanceAndClients(ctx, s.db, instanceID, clientIDs)
	} else {
		postgresSessions, err = s.sessionRepo.FindAllActiveSessionsByInstanceAndClients(ctx, s.db, instanceID, clientIDs)
	}
	if err != nil {
		return nil, err
	}

	postgresSessions = s.loadMaintenanceUpdatesIfNecessary(ctx, postgresSessions)

	// Copy over the Postgres Sessions over to a []*Session array
	resultSessions := make([]*Session, len(postgresSessions))
	for i, pgSession := range postgresSessions {
		newSession := &Session{}
		newSession.CopyFromSessionModel(pgSession)
		resultSessions[i] = newSession
	}
	return resultSessions, nil
}

func (s *postgresDataStore) loadMaintenanceUpdatesIfNecessary(ctx context.Context, postgresSessions []*model.Session) []*model.Session {
	if !maintenance.FromContext(ctx) {
		return postgresSessions
	}
	// for each of the given postgres sessions, check if we have any updates in Redis
	actualSessions := make([]*model.Session, len(postgresSessions))
	for i, session := range postgresSessions {
		var redisSession model.Session
		if err := s.cache.Get(ctx, maintenanceSessionKey(session.ID, session.InstanceID), &redisSession); err != nil {
			sentryclerk.CaptureException(ctx, err)
		}
		if redisSession.Session == nil {
			// no updates found in redis, let's use the postgres one
			actualSessions[i] = session
		} else {
			actualSessions[i] = &redisSession
		}
	}
	return actualSessions
}

func (s *postgresDataStore) CreateSession(ctx context.Context, instanceID string, clientID string, session *Session) error {
	postgresSession := &model.Session{Session: &sqbmodel.Session{}}
	session.CopyToSessionModel(postgresSession)
	postgresSession.InstanceID = instanceID
	postgresSession.ClientID = clientID
	err := s.sessionRepo.Insert(ctx, s.db, postgresSession)
	if err != nil {
		return err
	}
	session.CopyFromSessionModel(postgresSession)
	return nil
}

func (s *postgresDataStore) FindSession(ctx context.Context, instanceID, clientID, sessionID string) (*Session, error) {
	postgresSession, err := s.findSession(ctx, sessionID, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, clerkerrors.WithStacktrace("%s: %w", err.Error(), ErrNoRecords)
	} else if err != nil {
		return nil, err
	}
	if postgresSession.InstanceID != instanceID || postgresSession.ClientID != clientID {
		return nil, ErrNoRecords
	}
	session := &Session{}
	session.CopyFromSessionModel(postgresSession)
	return session, nil
}

func (s *postgresDataStore) UpdateSession(ctx context.Context, _, _ string, session *Session, cols ...string) error {
	postgresSession := &model.Session{Session: &sqbmodel.Session{}}
	session.CopyToSessionModel(postgresSession)

	var err error
	if maintenance.FromContext(ctx) {
		// we are in maintenance mode, let's use cache to store the updated session
		err = s.cache.Set(ctx, maintenanceSessionKey(postgresSession.ID, session.InstanceID), postgresSession, time.Hour)
	} else {
		err = s.sessionRepo.Update(ctx, s.db, postgresSession, false, cols...)
	}
	if err != nil {
		return err
	}
	session.CopyFromSessionModel(postgresSession)
	return nil
}

func (s *postgresDataStore) DeleteSession(ctx context.Context, instanceID, clientID, sessionID string) error {
	// Keeping a Transaction here to be able to handle cascading logic later
	return s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.sessionRepo.DeleteByInstanceAndClientAndID(ctx, tx, instanceID, clientID, sessionID)
		if err != nil {
			return true, err
		}
		return false, nil
	})
}

func (s *postgresDataStore) findSession(ctx context.Context, sessionID, instanceID string) (*model.Session, error) {
	inRecoveryMode := recovery.FromContext(ctx)
	if maintenance.FromContext(ctx) || inRecoveryMode {
		var postgresSession model.Session

		// We're in maintenance/recovery mode, so we first need to check Redis in case there were any
		// updates on that session during the maintenance period.
		if err := s.cache.Get(ctx, maintenanceSessionKey(sessionID, instanceID), &postgresSession); err != nil {
			return nil, err
		}
		if postgresSession.Session != nil {
			if inRecoveryMode {
				// update session in DB and cleanup redis entry, ignore error
				if err := s.sessionRepo.Update(ctx, s.db, &postgresSession, false); err != nil {
					sentryclerk.CaptureException(ctx, err)
				}
				if err := s.cache.Delete(ctx, maintenanceSessionKey(sessionID, instanceID)); err != nil {
					sentryclerk.CaptureException(ctx, err)
				}
			}
			return &postgresSession, nil
		}
	}
	postgresSession, err := s.sessionRepo.FindByID(ctx, s.db, sessionID)
	if err != nil {
		return nil, err
	} else if postgresSession.Status == constants.SESSPendingActivation {
		return nil, sql.ErrNoRows
	}
	return postgresSession, nil
}

func (s *postgresDataStore) findClient(ctx context.Context, clientID, instanceID string) (*model.Client, error) {
	inRecoveryMode := recovery.FromContext(ctx)
	if maintenance.FromContext(ctx) || inRecoveryMode {
		// We're in maintenance/recovery mode, so we first need to check Redis in case there were any
		// updates on that client during the maintenance period.
		var postgresClient model.Client
		if err := s.cache.Get(ctx, maintenanceClientKey(clientID, instanceID), &postgresClient); err != nil {
			return nil, err
		}
		if postgresClient.Client != nil {
			if inRecoveryMode {
				// update client in DB and cleanup redis entry, ignore error
				if err := s.clientRepo.Update(ctx, s.db, &postgresClient); err != nil {
					sentryclerk.CaptureException(ctx, err)
				}
				if err := s.cache.Delete(ctx, maintenanceClientKey(clientID, instanceID)); err != nil {
					sentryclerk.CaptureException(ctx, err)
				}
			}
			return &postgresClient, nil
		}
	}
	return s.clientRepo.FindByID(ctx, s.db, clientID)
}

func maintenanceSessionKey(sessionID, instanceID string) string {
	return fmt.Sprintf("maintenance:%s:%s", instanceID, sessionID)
}

func maintenanceClientKey(clientID, instanceID string) string {
	return fmt.Sprintf("maintenance:%s:%s", instanceID, clientID)
}
