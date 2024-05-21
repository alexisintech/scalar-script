package client_data

import (
	"context"
	"errors"
	"fmt"

	"clerk/pkg/cenv"
	"clerk/pkg/ctx/primed_edge_client_id"
	edge_client_service "clerk/pkg/edgeclientservice"
	clerktime "clerk/pkg/time"

	"github.com/volatiletech/null/v8"
)

type edgeClientDatastore struct {
	apiClient *edge_client_service.Client
}

func NewEdgeClientDatastore() DataStore {
	client := edge_client_service.NewEdgeClientServiceAPIClient(
		cenv.Get(cenv.EdgeClientServiceBaseURL),
		cenv.Get(cenv.EdgeClientServiceAdminToken),
	)
	return &edgeClientDatastore{
		apiClient: client,
	}
}

func (e *edgeClientDatastore) CreateClient(ctx context.Context, instanceID string, client *Client) error {
	if client.CookieValue.Valid {
		return fmt.Errorf("cannot create Client with non-empty CookieValue")
	}

	// If our primedEdgeClientID is provided in the context pass it along
	// as our durableObjectID so we create our Client in that object
	var durableObjectID *string
	primedEdgeClientID := primed_edge_client_id.FromContext(ctx)
	if primedEdgeClientID != "" {
		durableObjectID = &primedEdgeClientID
	}

	// Prepare the Request Body
	requestBody := edge_client_service.PostInstancesInstanceIdClientsBody{
		DurableObjectId:           durableObjectID,
		RotatingToken:             client.RotatingToken,
		Ended:                     &client.Ended,
		SignInId:                  client.SignInID.Ptr(),
		SignUpId:                  client.SignUpID.Ptr(),
		ToSignInAccountTransferId: client.ToSignInAccountTransferID.Ptr(),
		ToSignUpAccountTransferId: client.ToSignUpAccountTransferID.Ptr(),
		PostponeCookieUpdate:      &client.PostponeCookieUpdate,
		RotatingTokenNonce:        client.RotatingTokenNonce.Ptr(),
	}

	// Perform the request
	response, err := e.apiClient.CreateClient(edge_client_service.CreateClientRequest{
		InstanceId: instanceID,
		Data:       requestBody,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.CreateClient400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return NewErrBadRequest(err, messages)
		}
		// 409 Conflict
		if errors.As(err, &edge_client_service.CreateClient409{}) {
			return NewErrConflict(err)
		}
		return err
	}

	// Copy the response client back into the Client
	return EdgeClientServiceClientResponseToClient(response.Result, client)
}

func (e *edgeClientDatastore) FindClient(_ context.Context, instanceID, clientID string) (*Client, error) {
	response, err := e.apiClient.ReadClient(edge_client_service.ReadClientRequest{
		InstanceId: instanceID,
		ClientId:   clientID,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.ReadClient400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return nil, NewErrBadRequest(err, messages)
		}

		// 404 Not Found
		if errors.As(err, &edge_client_service.ReadClient404{}) {
			return nil, ErrNoRecords
		}

		return nil, err
	}

	// Convert the API Response into a Client
	responseClient := &Client{}
	err = EdgeClientServiceClientResponseToClient(response.Result, responseClient)
	if err != nil {
		return nil, err
	}

	// OK
	return responseClient, nil
}

func (e *edgeClientDatastore) UpdateClient(_ context.Context, instanceID string, client *Client, cols ...string) error {
	// Prepare an empty request body
	requestBody := edge_client_service.PatchInstancesInstanceIdClientsClientIdBody{}

	// Loop through all of the columns we want to update & fill the requestBody
	for _, column := range cols {
		switch column {
		case ClientColumns.CookieValue:
			requestBody.CookieValue = client.CookieValue.Ptr()

		case ClientColumns.RotatingToken:
			requestBody.RotatingToken = &client.RotatingToken

		case ClientColumns.Ended:
			requestBody.Ended = &client.Ended

		case ClientColumns.SignInID:
			requestBody.SignInId = &client.SignInID

		case ClientColumns.SignUpID:
			requestBody.SignUpId = &client.SignUpID

		case ClientColumns.ToSignInAccountTransferID:
			requestBody.ToSignInAccountTransferId = &client.ToSignInAccountTransferID

		case ClientColumns.ToSignUpAccountTransferID:
			requestBody.ToSignUpAccountTransferId = &client.ToSignUpAccountTransferID

		case ClientColumns.PostponeCookieUpdate:
			requestBody.PostponeCookieUpdate = &client.PostponeCookieUpdate

		case ClientColumns.RotatingTokenNonce:
			requestBody.RotatingTokenNonce = &client.RotatingTokenNonce
		}
	}

	// Perform the request
	response, err := e.apiClient.UpdateClient(edge_client_service.UpdateClientRequest{
		InstanceId: instanceID,
		ClientId:   client.ID,
		Data:       requestBody,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.UpdateClient400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return NewErrBadRequest(err, messages)
		}
		// 404 Not Found
		if errors.As(err, &edge_client_service.UpdateClient404{}) {
			return ErrNoRecords
		}

		return err
	}

	// Copy the response client back into the Client
	return EdgeClientServiceClientResponseToClient(response.Result, client)
}

func (e *edgeClientDatastore) DeleteClient(_ context.Context, instanceID, clientID string) error {
	_, err := e.apiClient.DeleteClient(edge_client_service.DeleteClientRequest{
		InstanceId: instanceID,
		ClientId:   clientID,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.DeleteClient400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return NewErrBadRequest(err, messages)
		}

		// 404 Not Found
		if errors.As(err, &edge_client_service.DeleteClient404{}) {
			return ErrNoRecords
		}

		// All others errors are unexpected
		return err
	}

	// OK
	return nil
}

func (e *edgeClientDatastore) FindClientByIDAndRotatingToken(ctx context.Context, instanceID, clientID, rotatingToken string) (*Client, error) {
	client, err := e.FindClient(ctx, instanceID, clientID)
	if err != nil {
		return nil, err
	}
	if client.RotatingToken != rotatingToken {
		return nil, ErrNoRecords
	}
	return client, nil
}

func (e *edgeClientDatastore) FindClientByIDAndRotatingTokenNonce(ctx context.Context, instanceID, clientID, rotatingTokenNonce string) (*Client, error) {
	client, err := e.FindClient(ctx, instanceID, clientID)
	if err != nil {
		return nil, err
	}
	if !client.RotatingTokenNonce.Valid || client.RotatingTokenNonce.String != rotatingTokenNonce {
		return nil, ErrNoRecords
	}
	return client, nil
}

func (e *edgeClientDatastore) FindUserSession(_ context.Context, instanceID, userID, sessionID string) (*Session, error) {
	response, err := e.apiClient.FetchUserSession(edge_client_service.FetchUserSessionRequest{
		InstanceId: instanceID,
		UserId:     userID,
		SessionId:  sessionID,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.FetchUserSession400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return nil, NewErrBadRequest(err, messages)
		}
		// 404 Not Found
		if errors.As(err, &edge_client_service.FetchUserSession404{}) {
			return nil, ErrNoRecords
		}
		// All others errors are unexpected
		return nil, err
	}

	// Convert the API Response into a Session
	responseSession := &Session{}
	err = EdgeClientServiceSessionResponseToSession(response.Result, responseSession)
	if err != nil {
		return nil, err
	}

	// OK
	return responseSession, nil
}

func (e *edgeClientDatastore) CreateSession(_ context.Context, instanceID, clientID string, session *Session) error {
	// Prepare the Request Body
	requestBody := edge_client_service.PostInstancesInstanceIdClientsClientIdSessionsBody{
		UserId:                   session.UserID,
		ExpireAt:                 clerktime.FormatTimeRFC3339Milli(session.ExpireAt),
		AbandonAt:                clerktime.FormatTimeRFC3339Milli(session.AbandonAt),
		TouchedAt:                clerktime.FormatTimeRFC3339Milli(session.TouchedAt),
		Status:                   (*edge_client_service.PostInstancesInstanceIdClientsClientIdSessionsBodyStatusEnum)(&session.Status),
		SessionInactivityTimeout: &session.SessionInactivityTimeout,
		ReplacementSessionId:     session.ReplacementSessionID.Ptr(),
		SessionActivityId:        session.SessionActivityID.Ptr(),
		ActiveOrganizationId:     session.ActiveOrganizationID.Ptr(),
	}

	if session.TokenIssuedAt.Valid {
		tokenIssuedAt := clerktime.FormatTimeRFC3339Milli(session.TokenIssuedAt.Time)
		requestBody.TokenIssuedAt = &tokenIssuedAt
	}
	if session.Actor.Valid {
		var actor map[string]interface{}
		err := session.Actor.Unmarshal(&actor)
		if err != nil {
			return err
		}
		requestBody.Actor = &actor
	}
	if session.TouchEventSentAt.Valid {
		touchEventSentAt := clerktime.FormatTimeRFC3339Milli(session.TouchEventSentAt.Time)
		requestBody.TouchEventSentAt = &touchEventSentAt
	}
	if session.TokenCreatedEventSentAt.Valid {
		tokenCreatedEventSentAt := clerktime.FormatTimeRFC3339Milli(session.TokenCreatedEventSentAt.Time)
		requestBody.TokenCreatedEventSentAt = &tokenCreatedEventSentAt
	}

	// Send the request
	response, err := e.apiClient.CreateSession(edge_client_service.CreateSessionRequest{
		InstanceId: instanceID,
		ClientId:   clientID,
		Data:       requestBody,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.CreateSession400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return NewErrBadRequest(err, messages)
		}
		// 404 Not Found
		if errors.As(err, &edge_client_service.CreateSession404{}) {
			return ErrNoRecords
		}
		// 409 Conflict
		if errors.As(err, &edge_client_service.CreateSession409{}) {
			return NewErrConflict(err)
		}
		return err
	}

	// Copy the response Session back into the Session
	return EdgeClientServiceSessionResponseToSession(response.Result, session)
}

func (e *edgeClientDatastore) FindSession(_ context.Context, instanceID, clientID, sessionID string) (*Session, error) {
	response, err := e.apiClient.FetchSession(edge_client_service.FetchSessionRequest{
		InstanceId: instanceID,
		ClientId:   clientID,
		SessionId:  sessionID,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.FetchSession400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return nil, NewErrBadRequest(err, messages)
		}

		// 404 Not Found
		if errors.As(err, &edge_client_service.FetchSession404{}) {
			return nil, ErrNoRecords
		}

		return nil, err
	}

	// Convert the API Response into a Session
	responseSession := &Session{}
	err = EdgeClientServiceSessionResponseToSession(response.Result, responseSession)
	if err != nil {
		return nil, err
	}

	// OK
	return responseSession, nil
}

func (e *edgeClientDatastore) UpdateSession(_ context.Context, instanceID, clientID string, session *Session, cols ...string) error {
	// Prepare an empty request body
	requestBody := edge_client_service.PatchInstancesInstanceIdClientsClientIdSessionsSessionIdBody{}

	// Loop through all of the columns we want to update & fill the requestBody
	for _, column := range cols {
		switch column {
		case SessionColumns.ReplacementSessionID:
			requestBody.ReplacementSessionId = &session.ReplacementSessionID

		case SessionColumns.ExpireAt:
			t := clerktime.FormatTimeRFC3339Milli(session.ExpireAt)
			requestBody.ExpireAt = &t

		case SessionColumns.AbandonAt:
			t := clerktime.FormatTimeRFC3339Milli(session.AbandonAt)
			requestBody.AbandonAt = &t

		case SessionColumns.TouchedAt:
			t := clerktime.FormatTimeRFC3339Milli(session.TouchedAt)
			requestBody.TouchedAt = &t

		case SessionColumns.SessionActivityID:
			requestBody.SessionActivityId = &session.SessionActivityID

		case SessionColumns.Status:
			status := edge_client_service.PatchInstancesInstanceIdClientsClientIdSessionsSessionIdBodyStatusEnum(session.Status)
			requestBody.Status = &status

		case SessionColumns.SessionInactivityTimeout:
			requestBody.SessionInactivityTimeout = &session.SessionInactivityTimeout

		case SessionColumns.ActiveOrganizationID:
			requestBody.ActiveOrganizationId = &session.ActiveOrganizationID

		case SessionColumns.TokenIssuedAt:
			if session.TokenIssuedAt.Valid {
				t := null.StringFrom(clerktime.FormatTimeRFC3339Milli(session.TokenIssuedAt.Time))
				requestBody.TokenIssuedAt = &t
			} else {
				requestBody.TokenIssuedAt = &null.String{}
			}

		case SessionColumns.TouchEventSentAt:
			if session.TouchEventSentAt.Valid {
				t := null.StringFrom(clerktime.FormatTimeRFC3339Milli(session.TouchEventSentAt.Time))
				requestBody.TouchEventSentAt = &t
			} else {
				requestBody.TouchEventSentAt = &null.String{}
			}

		case SessionColumns.TokenCreatedEventSentAt:
			if session.TokenCreatedEventSentAt.Valid {
				t := null.StringFrom(clerktime.FormatTimeRFC3339Milli(session.TokenCreatedEventSentAt.Time))
				requestBody.TokenCreatedEventSentAt = &t
			} else {
				requestBody.TokenCreatedEventSentAt = &null.String{}
			}
		}
	}

	// Perform the request
	response, err := e.apiClient.UpdateSession(edge_client_service.UpdateSessionRequest{
		InstanceId: instanceID,
		ClientId:   clientID,
		SessionId:  session.ID,
		Data:       requestBody,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.UpdateSession400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return NewErrBadRequest(err, messages)
		}
		// 404 Not Found
		if errors.As(err, &edge_client_service.UpdateSession404{}) {
			return ErrNoRecords
		}
		return err
	}

	// Copy the response session back into the session
	return EdgeClientServiceSessionResponseToSession(response.Result, session)
}

func (e *edgeClientDatastore) DeleteSession(_ context.Context, instanceID, clientID, sessionID string) error {
	_, err := e.apiClient.DeleteSession(edge_client_service.DeleteSessionRequest{
		InstanceId: instanceID,
		ClientId:   clientID,
		SessionId:  sessionID,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.DeleteSession400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return NewErrBadRequest(err, messages)
		}

		// 404 Not Found
		if errors.As(err, &edge_client_service.DeleteSession404{}) {
			return ErrNoRecords
		}

		return err
	}

	// OK
	return nil
}

func (e *edgeClientDatastore) FindAllUserSessions(_ context.Context, instanceID, userID string, filterParams *SessionFilterParams) ([]*Session, error) {
	activeOnly := edge_client_service.GetInstancesInstanceIdUsersUserIdSessionsActiveOnlyEnumFalse
	if filterParams.activeOnly() {
		activeOnly = edge_client_service.GetInstancesInstanceIdUsersUserIdSessionsActiveOnlyEnumTrue
	}
	response, err := e.apiClient.FetchAllUserSessions(edge_client_service.FetchAllUserSessionsRequest{
		InstanceId: instanceID,
		UserId:     userID,
		ActiveOnly: &activeOnly,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.FetchAllUserSessions400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return nil, NewErrBadRequest(err, messages)
		}

		// 404 Not Found
		if errors.As(err, &edge_client_service.FetchAllUserSessions404{}) {
			return nil, ErrNoRecords
		}

		// All others errors are unexpected
		return nil, err
	}

	// Convert the API Response Sessions into []Session
	userSessions := make([]*Session, len(response.Result))
	for i, sessionResponse := range response.Result {
		userSession := &Session{}
		err = EdgeClientServiceSessionResponseToSession(sessionResponse, userSession)
		if err != nil {
			return nil, err
		}
		userSessions[i] = userSession
	}

	// OK
	return userSessions, nil
}

func (e *edgeClientDatastore) FindAllClientSessions(_ context.Context, instanceID, clientID string, filterParams *SessionFilterParams) ([]*Session, error) {
	activeOnly := edge_client_service.GetInstancesInstanceIdClientsClientIdSessionsActiveOnlyEnumFalse
	if filterParams.activeOnly() {
		activeOnly = edge_client_service.GetInstancesInstanceIdClientsClientIdSessionsActiveOnlyEnumTrue
	}
	response, err := e.apiClient.FetchAllClientSessions(edge_client_service.FetchAllClientSessionsRequest{
		InstanceId: instanceID,
		ClientId:   clientID,
		ActiveOnly: &activeOnly,
	})
	if err != nil {
		// 400 Bad Request
		var error400 edge_client_service.FetchAllClientSessions400
		if errors.As(err, &error400) {
			messages := []string{}
			for _, paramErr := range error400.Data.Errors {
				messages = append(messages, paramErr.Msg)
			}
			return nil, NewErrBadRequest(err, messages)
		}

		// 404 Not Found
		if errors.As(err, &edge_client_service.FetchAllClientSessions404{}) {
			return nil, ErrNoRecords
		}

		// All others errors are unexpected
		return nil, err
	}

	// Convert the API Response Sessions into []Session
	clientSessions := make([]*Session, len(response.Result))
	for i, sessionResponse := range response.Result {
		session := &Session{}
		err = EdgeClientServiceSessionResponseToSession(sessionResponse, session)
		if err != nil {
			return nil, err
		}
		clientSessions[i] = session
	}

	// OK
	return clientSessions, nil
}

func (e *edgeClientDatastore) FindAllClientsSessions(ctx context.Context, instanceID string, clientIDs []string, filterParams *SessionFilterParams) ([]*Session, error) {
	allSessions := []*Session{}
	for _, clientID := range clientIDs {
		sessions, err := e.FindAllClientSessions(ctx, instanceID, clientID, filterParams)
		if err != nil {
			return nil, err
		}
		allSessions = append(allSessions, sessions...)
	}
	return allSessions, nil
}
