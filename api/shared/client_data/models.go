package client_data

import (
	"encoding/json"
	"time"

	"clerk/model"
	"clerk/model/sqbmodel"
	edge_client_service "clerk/pkg/edgeclientservice"
	clerktime "clerk/pkg/time"

	"github.com/volatiletech/null/v8"
)

type Client struct {
	ID                        string      `json:"id"`
	InstanceID                string      `json:"instance_id"`
	CookieValue               null.String `json:"cookie_value"`
	RotatingToken             string      `json:"rotating_token"`
	Ended                     bool        `json:"ended"`
	SignInID                  null.String `json:"sign_in_id,omitempty"`
	SignUpID                  null.String `json:"sign_up_id,omitempty"`
	ToSignInAccountTransferID null.String `json:"to_sign_in_account_transfer_id,omitempty"`
	ToSignUpAccountTransferID null.String `json:"to_sign_up_account_transfer_id,omitempty"`
	CreatedAt                 time.Time   `json:"created_at"`
	UpdatedAt                 time.Time   `json:"updated_at"`
	PostponeCookieUpdate      bool        `json:"postpone_cookie_update"`
	RotatingTokenNonce        null.String `json:"rotating_token_nonce,omitempty"`
}

// NewClientFromClientModel creates a new Client from a model.Client instance
func NewClientFromClientModel(pgClient *model.Client) *Client {
	c := &Client{}
	c.CopyFromClientModel(pgClient)
	return c
}

// CopyFromClientModel copies over a *model.Client values over to
// our Client. This is needed to convert the result of our Postgres repositories
// back to our Client object before returning back to the user.
func (client *Client) CopyFromClientModel(pgClient *model.Client) {
	if pgClient == nil {
		return // Return early, there's nothing to copy from
	}
	client.ID = pgClient.ID
	client.InstanceID = pgClient.InstanceID
	client.CookieValue = pgClient.CookieValue
	client.RotatingToken = pgClient.RotatingToken
	client.Ended = pgClient.Ended
	client.SignInID = pgClient.SignInID
	client.SignUpID = pgClient.SignUpID
	client.ToSignInAccountTransferID = pgClient.ToSignInAccountTransferID
	client.ToSignUpAccountTransferID = pgClient.ToSignUpAccountTransferID
	client.CreatedAt = pgClient.CreatedAt
	client.UpdatedAt = pgClient.UpdatedAt
	client.PostponeCookieUpdate = pgClient.PostponeCookieUpdate
	client.RotatingTokenNonce = pgClient.RotatingTokenNonce
}

// CopyToClientModel copies over a *Client values over to a *model.Client
func (client *Client) CopyToClientModel(pgClient *model.Client) {
	if pgClient == nil {
		return // Return early, there's nothing to copy into
	}
	pgClient.ID = client.ID
	pgClient.InstanceID = client.InstanceID
	pgClient.CookieValue = client.CookieValue
	pgClient.RotatingToken = client.RotatingToken
	pgClient.Ended = client.Ended
	pgClient.SignInID = client.SignInID
	pgClient.SignUpID = client.SignUpID
	pgClient.ToSignInAccountTransferID = client.ToSignInAccountTransferID
	pgClient.ToSignUpAccountTransferID = client.ToSignUpAccountTransferID
	pgClient.CreatedAt = client.CreatedAt
	pgClient.UpdatedAt = client.UpdatedAt
	pgClient.PostponeCookieUpdate = client.PostponeCookieUpdate
	pgClient.RotatingTokenNonce = client.RotatingTokenNonce
}

// ToClientModel creates a new *model.Client and copies over the values
// of our *Client over to it.
func (client *Client) ToClientModel() *model.Client {
	clientModel := &model.Client{Client: &sqbmodel.Client{}}
	client.CopyToClientModel(clientModel)
	return clientModel
}

var ClientColumns = struct {
	ID                        string
	InstanceID                string
	CookieValue               string
	RotatingToken             string
	Ended                     string
	SignInID                  string
	SignUpID                  string
	ToSignInAccountTransferID string
	ToSignUpAccountTransferID string
	CreatedAt                 string
	UpdatedAt                 string
	PostponeCookieUpdate      string
	RotatingTokenNonce        string
}{
	ID:                        "id",
	InstanceID:                "instance_id",
	CookieValue:               "cookie_value",
	RotatingToken:             "rotating_token",
	Ended:                     "ended",
	SignInID:                  "sign_in_id",
	SignUpID:                  "sign_up_id",
	ToSignInAccountTransferID: "to_sign_in_account_transfer_id",
	ToSignUpAccountTransferID: "to_sign_up_account_transfer_id",
	CreatedAt:                 "created_at",
	UpdatedAt:                 "updated_at",
	PostponeCookieUpdate:      "postpone_cookie_update",
	RotatingTokenNonce:        "rotating_token_nonce",
}

type Session struct {
	ID                       string      `json:"id"`
	InstanceID               string      `json:"instance_id"`
	ClientID                 string      `json:"client_id"`
	UserID                   string      `json:"user_id"`
	ReplacementSessionID     null.String `json:"replacement_session_id,omitempty"`
	ExpireAt                 time.Time   `json:"expire_at"`
	AbandonAt                time.Time   `json:"abandon_at"`
	TouchedAt                time.Time   `json:"touched_at"`
	CreatedAt                time.Time   `json:"created_at"`
	UpdatedAt                time.Time   `json:"updated_at"`
	SessionActivityID        null.String `json:"session_activity_id,omitempty"`
	Status                   string      `json:"status"`
	SessionInactivityTimeout int         `json:"session_inactivity_timeout"`
	TokenIssuedAt            null.Time   `json:"token_issued_at,omitempty"`
	ActiveOrganizationID     null.String `json:"active_organization_id,omitempty"`
	Actor                    null.JSON   `json:"actor,omitempty"`
	TouchEventSentAt         null.Time   `json:"touch_event_sent_at,omitempty"`
	TokenCreatedEventSentAt  null.Time   `json:"token_created_event_sent_at,omitempty"`
}

// NewSessionFromSessionModel creates a new Session from a model.Session instance
func NewSessionFromSessionModel(pgSession *model.Session) *Session {
	s := &Session{}
	s.CopyFromSessionModel(pgSession)
	return s
}

// CopyFromSessionModel copies over a *model.Session values over to
// our Session. This is needed to convert the result of our Postgres repositories
// back to our Session object before returning back to the user.
func (session *Session) CopyFromSessionModel(pgSession *model.Session) {
	if pgSession == nil {
		return // Return early, there's nothing to copy from
	}
	session.ID = pgSession.ID
	session.InstanceID = pgSession.InstanceID
	session.ClientID = pgSession.ClientID
	session.UserID = pgSession.UserID
	session.ReplacementSessionID = pgSession.ReplacementSessionID
	session.ExpireAt = pgSession.ExpireAt
	session.AbandonAt = pgSession.AbandonAt
	session.TouchedAt = pgSession.TouchedAt
	session.CreatedAt = pgSession.CreatedAt
	session.UpdatedAt = pgSession.UpdatedAt
	session.SessionActivityID = pgSession.SessionActivityID
	session.Status = pgSession.Status
	session.SessionInactivityTimeout = pgSession.SessionInactivityTimeout
	session.TokenIssuedAt = pgSession.TokenIssuedAt
	session.ActiveOrganizationID = pgSession.ActiveOrganizationID
	session.Actor = pgSession.Actor
	session.TouchEventSentAt = pgSession.TouchEventSentAt
	session.TokenCreatedEventSentAt = pgSession.TokenCreatedEventSentAt
}

// CopyToSessionModel copies over a *Session values over to a *model.Session
func (session *Session) CopyToSessionModel(pgSession *model.Session) {
	if pgSession == nil {
		return // Return early, there's nothing to copy into
	}
	pgSession.ID = session.ID
	pgSession.InstanceID = session.InstanceID
	pgSession.ClientID = session.ClientID
	pgSession.UserID = session.UserID
	pgSession.ReplacementSessionID = session.ReplacementSessionID
	pgSession.ExpireAt = session.ExpireAt
	pgSession.AbandonAt = session.AbandonAt
	pgSession.TouchedAt = session.TouchedAt
	pgSession.CreatedAt = session.CreatedAt
	pgSession.UpdatedAt = session.UpdatedAt
	pgSession.SessionActivityID = session.SessionActivityID
	pgSession.Status = session.Status
	pgSession.SessionInactivityTimeout = session.SessionInactivityTimeout
	pgSession.TokenIssuedAt = session.TokenIssuedAt
	pgSession.ActiveOrganizationID = session.ActiveOrganizationID
	pgSession.Actor = session.Actor
	pgSession.TouchEventSentAt = session.TouchEventSentAt
	pgSession.TokenCreatedEventSentAt = session.TokenCreatedEventSentAt
}

// ToSessionModel creates a new *model.Session and copies over the values
// of our *Session over to it.
func (session *Session) ToSessionModel() *model.Session {
	sessionModel := &model.Session{Session: &sqbmodel.Session{}}
	session.CopyToSessionModel(sessionModel)
	return sessionModel
}

var SessionColumns = struct {
	ID                       string
	InstanceID               string
	ClientID                 string
	UserID                   string
	ReplacementSessionID     string
	ExpireAt                 string
	AbandonAt                string
	TouchedAt                string
	CreatedAt                string
	UpdatedAt                string
	SessionActivityID        string
	Status                   string
	SessionInactivityTimeout string
	TokenIssuedAt            string
	ActiveOrganizationID     string
	Actor                    string
	TouchEventSentAt         string
	TokenCreatedEventSentAt  string
}{
	ID:                       "id",
	InstanceID:               "instance_id",
	ClientID:                 "client_id",
	UserID:                   "user_id",
	ReplacementSessionID:     "replacement_session_id",
	ExpireAt:                 "expire_at",
	AbandonAt:                "abandon_at",
	TouchedAt:                "touched_at",
	CreatedAt:                "created_at",
	UpdatedAt:                "updated_at",
	SessionActivityID:        "session_activity_id",
	Status:                   "status",
	SessionInactivityTimeout: "session_inactivity_timeout",
	TokenIssuedAt:            "token_issued_at",
	ActiveOrganizationID:     "active_organization_id",
	Actor:                    "actor",
	TouchEventSentAt:         "touch_event_sent_at",
	TokenCreatedEventSentAt:  "token_created_event_sent_at",
}

// ToSessionModels convert all the elements of a *Session slice to *model.Session
func ToSessionModels(sessions []*Session) []*model.Session {
	modelSessions := make([]*model.Session, 0, len(sessions))
	for _, s := range sessions {
		modelSessions = append(modelSessions, s.ToSessionModel())
	}
	return modelSessions
}

// EdgeClientServiceClientResponseToClient copies a response Client from the Edge Client Service into a *Client
func EdgeClientServiceClientResponseToClient(resp edge_client_service.ClientResponse, client *Client) error {
	// Parse out times
	createdAtTime, err := clerktime.ParseTimestampRFC3339Milli(resp.CreatedAt)
	if err != nil {
		return err
	}
	updatedAtTime, err := clerktime.ParseTimestampRFC3339Milli(resp.UpdatedAt)
	if err != nil {
		return err
	}

	// Copy the values over
	client.ID = resp.Id
	client.InstanceID = resp.InstanceId
	client.CookieValue = null.StringFromPtr(resp.CookieValue)
	client.RotatingToken = resp.RotatingToken
	client.Ended = resp.Ended
	client.SignInID = null.StringFromPtr(resp.SignInId)
	client.SignUpID = null.StringFromPtr(resp.SignUpId)
	client.ToSignInAccountTransferID = null.StringFromPtr(resp.ToSignInAccountTransferId)
	client.ToSignUpAccountTransferID = null.StringFromPtr(resp.ToSignUpAccountTransferId)
	client.CreatedAt = createdAtTime
	client.UpdatedAt = updatedAtTime
	client.PostponeCookieUpdate = resp.PostponeCookieUpdate
	client.RotatingTokenNonce = null.StringFromPtr(resp.RotatingTokenNonce)
	return nil
}

// EdgeClientServiceSessionResponseToSession copies a response Session from the Edge Client Service into a *Session
func EdgeClientServiceSessionResponseToSession(resp edge_client_service.SessionResponse, session *Session) error {
	// Parse out times
	expireAtTime, err := clerktime.ParseTimestampRFC3339Milli(resp.ExpireAt)
	if err != nil {
		return err
	}
	abandonAtTime, err := clerktime.ParseTimestampRFC3339Milli(resp.AbandonAt)
	if err != nil {
		return err
	}
	touchedAtTime, err := clerktime.ParseTimestampRFC3339Milli(resp.TouchedAt)
	if err != nil {
		return err
	}
	createdAtTime, err := clerktime.ParseTimestampRFC3339Milli(resp.CreatedAt)
	if err != nil {
		return err
	}
	updatedAtTime, err := clerktime.ParseTimestampRFC3339Milli(resp.UpdatedAt)
	if err != nil {
		return err
	}

	// Parse out the Nullable times & set them if they are non-null
	if resp.TokenIssuedAt != nil {
		tokenIssuedAtTime, err := clerktime.ParseTimestampRFC3339Milli(*resp.TokenIssuedAt)
		if err != nil {
			return err
		}
		session.TokenIssuedAt = null.TimeFrom(tokenIssuedAtTime)
	}
	if resp.TouchEventSentAt != nil {
		touchEventSentAtTime, err := clerktime.ParseTimestampRFC3339Milli(*resp.TouchEventSentAt)
		if err != nil {
			return err
		}
		session.TouchEventSentAt = null.TimeFrom(touchEventSentAtTime)
	}
	if resp.TokenCreatedEventSentAt != nil {
		tokenCreatedEventSentAtTime, err := clerktime.ParseTimestampRFC3339Milli(*resp.TokenCreatedEventSentAt)
		if err != nil {
			return err
		}
		session.TokenCreatedEventSentAt = null.TimeFrom(tokenCreatedEventSentAtTime)
	}

	// Parse out the Actor if it's not null
	if resp.Actor != nil {
		bytes, err := json.Marshal(resp.Actor)
		if err != nil {
			return err
		}
		session.Actor = null.JSONFrom(bytes)
	}

	session.ID = resp.Id
	session.InstanceID = resp.InstanceId
	session.ClientID = resp.ClientId
	session.UserID = resp.UserId
	session.ReplacementSessionID = null.StringFromPtr(resp.ReplacementSessionId)
	session.ExpireAt = expireAtTime
	session.AbandonAt = abandonAtTime
	session.TouchedAt = touchedAtTime
	session.CreatedAt = createdAtTime
	session.UpdatedAt = updatedAtTime
	session.SessionActivityID = null.StringFromPtr(resp.SessionActivityId)
	session.Status = string(resp.Status)
	session.SessionInactivityTimeout = resp.SessionInactivityTimeout
	session.ActiveOrganizationID = null.StringFromPtr(resp.ActiveOrganizationId)
	return nil
}
