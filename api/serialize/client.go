package serialize

import (
	"context"

	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/time"
	usersettings "clerk/pkg/usersettings/clerk"

	"github.com/jonboulle/clockwork"
)

type ClientResponseServerAPI struct {
	Object              string                   `json:"object"`
	ID                  string                   `json:"id"`
	SessionIDs          []string                 `json:"session_ids"`
	Sessions            []*SessionServerResponse `json:"sessions"`
	SignInID            *string                  `json:"sign_in_id"`
	SignUpID            *string                  `json:"sign_up_id"`
	LastActiveSessionID *string                  `json:"last_active_session_id"`
	CreatedAt           int64                    `json:"created_at"`
	UpdatedAt           int64                    `json:"updated_at"`
}

type ClientResponseClientAPI struct {
	Object              string                   `json:"object"`
	ID                  string                   `json:"id"`
	Sessions            []*SessionClientResponse `json:"sessions"`
	SignIn              *SignInResponse          `json:"sign_in"`
	SignUp              *SignUpResponse          `json:"sign_up"`
	LastActiveSessionID *string                  `json:"last_active_session_id"`
	CreatedAt           int64                    `json:"created_at"`
	UpdatedAt           int64                    `json:"updated_at"`
}

func ClientToServerAPI(clock clockwork.Clock, client *model.ClientWithSessionsForBAPI) *ClientResponseServerAPI {
	response := ClientResponseServerAPI{
		Object:    "client",
		ID:        client.ID,
		CreatedAt: time.UnixMilli(client.CreatedAt),
		UpdatedAt: time.UnixMilli(client.UpdatedAt),
	}

	sessionIDs := make([]string, len(client.CurrentSessions))
	sessions := make([]*SessionServerResponse, len(client.CurrentSessions))
	for i := range client.CurrentSessions {
		sessionIDs[i] = client.CurrentSessions[i].ID
		sessions[i] = SessionToServerAPI(clock, client.CurrentSessions[i])
	}

	response.SessionIDs = sessionIDs
	response.Sessions = sessions

	if client.LastActiveSessionID != nil {
		response.LastActiveSessionID = client.LastActiveSessionID
	}

	if client.SignInID.Valid {
		response.SignInID = &client.SignInID.String
	}

	if client.SignUpID.Valid {
		response.SignUpID = &client.SignUpID.String
	}

	return &response
}

func ClientToClientAPI(
	ctx context.Context,
	clock clockwork.Clock,
	client *model.ClientWithSessions,
	userSettings *usersettings.UserSettings) (*ClientResponseClientAPI, error) {
	if client == nil {
		return nil, nil
	}

	sessionResponses := make([]*SessionClientResponse, 0)
	for i := range client.CurrentSessions {
		sessionResponse, err := SessionToClientAPI(ctx, clock, client.CurrentSessions[i])
		if err != nil {
			return nil, err
		}
		if client.CurrentSessions[i].Token != "" {
			sessionResponse.Token = Token(client.CurrentSessions[i].Token)
		}

		sessionResponses = append(sessionResponses, sessionResponse)
	}

	response := ClientResponseClientAPI{
		Object:    "client",
		ID:        client.ID,
		Sessions:  sessionResponses,
		CreatedAt: time.UnixMilli(client.CreatedAt),
		UpdatedAt: time.UnixMilli(client.UpdatedAt),
	}

	if client.LastActiveSession != nil {
		response.LastActiveSessionID = &client.LastActiveSession.ID
	}

	if client.SignIn != nil {
		if client.SignIn.SignIn.Status(clock) != constants.SignUpAbandoned {
			signInResponse, err := SignIn(clock, client.SignIn, userSettings)
			if err != nil {
				return nil, err
			}
			response.SignIn = signInResponse
		}
	}

	if client.SignUp != nil {
		if client.SignUp.Status(clock) != constants.SignUpAbandoned {
			signUpResponse, err := SignUp(ctx, clock, client.SignUp)
			if err != nil {
				return nil, err
			}
			response.SignUp = signUpResponse
		}
	}

	return &response, nil
}
