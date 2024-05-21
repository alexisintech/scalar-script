package client_data

import (
	"context"

	"clerk/pkg/cenv"
	"clerk/utils/clerk"
)

// IsEdgeID check if an entity ID belongs to Cloudflare or Postgres.
// An Edge ID a `prefix_` followed by 64 hex characters
// An Origin ID is a `prefix_` followed by 27 characters (KSUID) format
func IsEdgeID(id string) bool {
	return len(id) >= 64
}

// A transitionDatastore is a DataStore implementation which uses
// both our Postgres & edgeClient DataStore implementations, and
// sends along the request to the appropriate datastore.
type transitionDatastore struct {
	postgresDatastore   DataStore
	edgeClientDatastore DataStore
}

func newTransitionDatastore(deps clerk.Deps) DataStore {
	return &transitionDatastore{
		postgresDatastore:   newPostgresDatastore(deps),
		edgeClientDatastore: NewEdgeClientDatastore(),
	}
}

func (e *transitionDatastore) resolveDatastore(id string) DataStore {
	if IsEdgeID(id) {
		return e.edgeClientDatastore
	}
	return e.postgresDatastore
}

func (e *transitionDatastore) CreateClient(ctx context.Context, instanceID string, client *Client) error {
	// If the feature flag is enabled for the Instance to have its Clients & Sessions to
	// be managed at Edge, we will create this Client at edge.
	//
	// Note: from a long term perspective, we do not want to create Edge Clients from Origin,
	// really under any circumstances, as that will place the Durable Object near Origin rather
	// than near the user. We are doing this now, as it will help us get to testing things out
	// end-to-end sooner rather than later.
	if cenv.ResourceHasAccess(cenv.FlagEdgeClientsEnabledInstanceIDs, instanceID) {
		return e.edgeClientDatastore.CreateClient(ctx, instanceID, client)
	}
	// Create the client from Postgres for all other instances
	return e.postgresDatastore.CreateClient(ctx, instanceID, client)
}

func (e *transitionDatastore) FindClient(ctx context.Context, instanceID, clientID string) (*Client, error) {
	return e.resolveDatastore(clientID).FindClient(ctx, instanceID, clientID)
}

func (e *transitionDatastore) UpdateClient(ctx context.Context, instanceID string, client *Client, cols ...string) error {
	return e.resolveDatastore(client.ID).UpdateClient(ctx, instanceID, client, cols...)
}

func (e *transitionDatastore) DeleteClient(ctx context.Context, instanceID, clientID string) error {
	return e.resolveDatastore(clientID).DeleteClient(ctx, instanceID, clientID)
}

func (e *transitionDatastore) FindClientByIDAndRotatingToken(ctx context.Context, instanceID, clientID, rotatingToken string) (*Client, error) {
	return e.resolveDatastore(clientID).FindClientByIDAndRotatingToken(ctx, instanceID, clientID, rotatingToken)
}

func (e *transitionDatastore) FindClientByIDAndRotatingTokenNonce(ctx context.Context, instanceID, clientID, rotatingTokenNonce string) (*Client, error) {
	return e.resolveDatastore(clientID).FindClientByIDAndRotatingTokenNonce(ctx, instanceID, clientID, rotatingTokenNonce)
}

func (e *transitionDatastore) FindUserSession(ctx context.Context, instanceID, userID, sessionID string) (*Session, error) {
	return e.resolveDatastore(sessionID).FindUserSession(ctx, instanceID, userID, sessionID)
}

func (e *transitionDatastore) CreateSession(ctx context.Context, instanceID, clientID string, session *Session) error {
	return e.resolveDatastore(clientID).CreateSession(ctx, instanceID, clientID, session)
}

func (e *transitionDatastore) FindSession(ctx context.Context, instanceID, clientID, sessionID string) (*Session, error) {
	return e.resolveDatastore(clientID).FindSession(ctx, instanceID, clientID, sessionID)
}

func (e *transitionDatastore) UpdateSession(ctx context.Context, instanceID, clientID string, session *Session, cols ...string) error {
	return e.resolveDatastore(clientID).UpdateSession(ctx, instanceID, clientID, session, cols...)
}

func (e *transitionDatastore) DeleteSession(ctx context.Context, instanceID, clientID, sessionID string) error {
	return e.resolveDatastore(clientID).DeleteSession(ctx, instanceID, clientID, sessionID)
}

func (e *transitionDatastore) FindAllUserSessions(ctx context.Context, instanceID, userID string, filterParams *SessionFilterParams) ([]*Session, error) {
	// If the Instance is enabled at Edge fetch the user sessions from Edge
	if cenv.ResourceHasAccess(cenv.FlagEdgeClientsEnabledInstanceIDs, instanceID) {
		edgeSessions, err := e.edgeClientDatastore.FindAllUserSessions(ctx, instanceID, userID, filterParams)
		if err != nil {
			return nil, err
		}
		// Note: This approach assumes a hard-cutoff for all instances, where Clients & Sessions
		// can only belong either at Edge or Origin. This will require us to shut down Clients
		// at Origin when we enable instances @ Edge.
		return edgeSessions, nil
	}

	// Fetch the Sessions from Postgres for all other instances
	postgresSessions, err := e.postgresDatastore.FindAllUserSessions(ctx, instanceID, userID, filterParams)
	if err != nil {
		return nil, err
	}
	return postgresSessions, nil
}

func (e *transitionDatastore) FindAllClientSessions(ctx context.Context, instanceID, clientID string, filterParams *SessionFilterParams) ([]*Session, error) {
	return e.resolveDatastore(clientID).FindAllClientSessions(ctx, instanceID, clientID, filterParams)
}

func (e *transitionDatastore) FindAllClientsSessions(ctx context.Context, instanceID string, clientIDs []string, filterParams *SessionFilterParams) ([]*Session, error) {
	// If no ClientIDs are passed in skip running any queries
	if len(clientIDs) == 0 {
		return []*Session{}, nil
	}

	// Determine where our Clients are to see if we can just query against one datastore.
	edgeOnly := true
	postgresOnly := true
	for _, clientID := range clientIDs {
		if IsEdgeID(clientID) {
			postgresOnly = false
		} else {
			edgeOnly = false
		}
	}
	if edgeOnly {
		return e.edgeClientDatastore.FindAllClientsSessions(ctx, instanceID, clientIDs, filterParams)
	} else if postgresOnly {
		return e.postgresDatastore.FindAllClientsSessions(ctx, instanceID, clientIDs, filterParams)
	}

	// IDs from both datastores were passed in, so we defer determining where to call
	// to the FindAllClientSessions on a per-client basis.
	allSessions := []*Session{}
	for i := 0; i < len(clientIDs); i++ {
		sessions, err := e.FindAllClientSessions(ctx, instanceID, clientIDs[i], filterParams)
		if err != nil {
			return nil, err
		}
		allSessions = append(allSessions, sessions...)
	}
	return allSessions, nil
}
