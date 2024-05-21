package client_data

import (
	"encoding/json"
	"testing"

	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	edge_client_service "clerk/pkg/edgeclientservice"
	"clerk/pkg/rand"
	"clerk/pkg/strings"
	clerktime "clerk/pkg/time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/volatiletech/null/v8"
)

func StructToMap(obj interface{}) (newMap map[string]interface{}, err error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return
	}
	err = json.Unmarshal(data, &newMap)
	return
}

func TestModelConversion(t *testing.T) {
	t.Parallel()

	// Create a basic client object
	client := &Client{
		ID:          rand.InternalClerkID(constants.IDPClient),
		InstanceID:  rand.InternalClerkID(constants.IDPInstance),
		CookieValue: null.StringFrom("some-cookie-value"),
		Ended:       false,
	}

	// Copy to SQL Boiler Client
	sqlBoilerClient := &model.Client{Client: &sqbmodel.Client{}}
	client.CopyToClientModel(sqlBoilerClient)

	// Test that the fields have been copied over
	assert.Equal(t, client.ID, sqlBoilerClient.ID)
	assert.Equal(t, client.InstanceID, sqlBoilerClient.InstanceID)
	assert.Equal(t, client.CookieValue, sqlBoilerClient.CookieValue)
	assert.Equal(t, client.Ended, sqlBoilerClient.Ended)
	assert.Equal(t, client.RotatingTokenNonce, sqlBoilerClient.RotatingTokenNonce)

	// Updates some fields
	sqlBoilerClient.Ended = true
	sqlBoilerClient.CookieValue = null.StringFrom("some-new-cookie-value")

	// Ensure that the fields are not equal, as we have not copied them back yet
	assert.NotEqual(t, client.Ended, sqlBoilerClient.Ended)
	assert.NotEqual(t, client.CookieValue, sqlBoilerClient.CookieValue)

	// Copy back
	client.CopyFromClientModel(sqlBoilerClient)

	// Test that the Objects are now equal.
	// We remove Client.L / Client.R as these objects are not
	// part of our basic Client.
	sqlBoilerMap, err := StructToMap(sqlBoilerClient.Client)
	require.NoError(t, err)
	delete(sqlBoilerMap, "L")
	delete(sqlBoilerMap, "R")
	clientMap, err := StructToMap(client)
	require.NoError(t, err)
	assert.Equal(t, sqlBoilerMap, clientMap)
}

func TestEdgeClientServiceClientResponseToClient(t *testing.T) {
	t.Parallel()

	// A timestamp we can use for our created at & updated at
	createdAtString := "2024-02-23T20:19:31.005Z"
	createdAtTime, err := clerktime.ParseTimestampRFC3339Milli(createdAtString)
	require.NoError(t, err)

	updatedAtString := "2024-02-23T20:19:32.005Z"
	updatedAtTime, err := clerktime.ParseTimestampRFC3339Milli(updatedAtString)
	require.NoError(t, err)

	// Prepare some values for pointers
	signInID := rand.InternalClerkID("sia")
	signUpID := rand.InternalClerkID("sua")
	siAccountTransfer := rand.InternalClerkID("acc_transfer")
	suAccountTransfer := rand.InternalClerkID("acc_transfer")
	rotatingTokenNonce, err := rand.Token()
	require.NoError(t, err)

	// Create a mock Client response with everything set
	everythingSetClientResponse := edge_client_service.ClientResponse{
		Id:                        "client_37467063d1c3f7eb2e0f6f5bebe1c7b68d601dd83523e5afe33419abc332b3f2",
		InstanceId:                "ins_1lyWDZiobr600AKUeQDoSlrEmoM",
		CookieValue:               strings.ToPtr("some-cookie-value"),
		RotatingToken:             "some-rotating-token",
		Ended:                     false,
		SignInId:                  &signInID,
		SignUpId:                  &signUpID,
		ToSignInAccountTransferId: &siAccountTransfer,
		ToSignUpAccountTransferId: &suAccountTransfer,
		PostponeCookieUpdate:      false,
		RotatingTokenNonce:        &rotatingTokenNonce,
		CreatedAt:                 createdAtString,
		UpdatedAt:                 updatedAtString,
	}

	// Copy it & test equality
	client := &Client{}
	err = EdgeClientServiceClientResponseToClient(everythingSetClientResponse, client)
	require.NoError(t, err)
	assert.Equal(t, &Client{
		ID:                        "client_37467063d1c3f7eb2e0f6f5bebe1c7b68d601dd83523e5afe33419abc332b3f2",
		InstanceID:                "ins_1lyWDZiobr600AKUeQDoSlrEmoM",
		CookieValue:               null.StringFrom("some-cookie-value"),
		RotatingToken:             "some-rotating-token",
		Ended:                     false,
		SignInID:                  null.StringFrom(signInID),
		SignUpID:                  null.StringFrom(signUpID),
		ToSignInAccountTransferID: null.StringFrom(siAccountTransfer),
		ToSignUpAccountTransferID: null.StringFrom(suAccountTransfer),
		PostponeCookieUpdate:      false,
		RotatingTokenNonce:        null.StringFrom(rotatingTokenNonce),
		CreatedAt:                 createdAtTime,
		UpdatedAt:                 updatedAtTime,
	}, client)

	// Create a mock Client response with all optionals set as nil
	allOptionalsAsNilClientResponse := edge_client_service.ClientResponse{
		Id:                   "client_37467063d1c3f7eb2e0f6f5bebe1c7b68d601dd83523e5afe33419abc332b3f2",
		InstanceId:           "ins_1lyWDZiobr600AKUeQDoSlrEmoM",
		CookieValue:          strings.ToPtr("some-cookie-value"),
		RotatingToken:        "some-rotating-token",
		Ended:                false,
		PostponeCookieUpdate: false,
		CreatedAt:            createdAtString,
		UpdatedAt:            updatedAtString,
	}

	// Copy it & test equality
	clientWithNulls := &Client{}
	err = EdgeClientServiceClientResponseToClient(allOptionalsAsNilClientResponse, clientWithNulls)
	require.NoError(t, err)
	assert.Equal(t, &Client{
		ID:                   "client_37467063d1c3f7eb2e0f6f5bebe1c7b68d601dd83523e5afe33419abc332b3f2",
		InstanceID:           "ins_1lyWDZiobr600AKUeQDoSlrEmoM",
		CookieValue:          null.StringFrom("some-cookie-value"),
		RotatingToken:        "some-rotating-token",
		Ended:                false,
		PostponeCookieUpdate: false,
		CreatedAt:            createdAtTime,
		UpdatedAt:            updatedAtTime,
	}, clientWithNulls)
}

func TestEdgeClientServiceSessionResponseToSession(t *testing.T) {
	t.Parallel()

	// Create some timestamps
	expireAtString := "2024-02-23T20:19:25.005Z"
	expireAtTime, err := clerktime.ParseTimestampRFC3339Milli(expireAtString)
	require.NoError(t, err)

	abandonAtString := "2024-02-23T20:19:26.005Z"
	abandonAtTime, err := clerktime.ParseTimestampRFC3339Milli(abandonAtString)
	require.NoError(t, err)

	touchedAtString := "2024-02-23T20:19:27.005Z"
	touchedAtTime, err := clerktime.ParseTimestampRFC3339Milli(touchedAtString)
	require.NoError(t, err)

	tokenIssuedAtString := "2024-02-23T20:19:28.005Z" // nolint:gosec
	tokenIssuedAtTime, err := clerktime.ParseTimestampRFC3339Milli(tokenIssuedAtString)
	require.NoError(t, err)

	touchEventSentAtString := "2024-02-23T20:19:29.005Z"
	touchEventSentAtTime, err := clerktime.ParseTimestampRFC3339Milli(touchEventSentAtString)
	require.NoError(t, err)

	tokenCreatedEventSentAtString := "2024-02-23T20:19:30.005Z" // nolint:gosec
	tokenCreatedEventSentAtTime, err := clerktime.ParseTimestampRFC3339Milli(tokenCreatedEventSentAtString)
	require.NoError(t, err)

	createdAtString := "2024-02-23T20:19:31.005Z"
	createdAtTime, err := clerktime.ParseTimestampRFC3339Milli(createdAtString)
	require.NoError(t, err)

	updatedAtString := "2024-02-23T20:19:32.005Z"
	updatedAtTime, err := clerktime.ParseTimestampRFC3339Milli(updatedAtString)
	require.NoError(t, err)

	// Prepare some variables for pointers
	replacementSessionID := "sess_5fe2c9393087f743d69c1ec3b044e5c18103eddc92bc75e3232d6f582f169d83"
	sessionActivityID := rand.InternalClerkID("sid")
	activeOrganizationID := rand.InternalClerkID("org")
	actor := map[string]any{"sub": "user_admin"}
	actorBytes, err := json.Marshal(actor)
	require.NoError(t, err)

	// Create a mock response with everything set
	everythingSetSessionResponse := edge_client_service.SessionResponse{
		Id:                       "sess_5fe2c9393087f743d69c1ec3b044e5c18103eddc92bc75e3232d6f582f169d82",
		InstanceId:               "ins_1lyWDZiobr600AKUeQDoSlrEmoM",
		ClientId:                 "client_66fc04fb4a09077d0a67f64add71d7b0237c1e34756d0223aa783caeaef143a9",
		UserId:                   "user_2cea06s4VcAoU7XZgfFcscdTjxB",
		ReplacementSessionId:     &replacementSessionID,
		ExpireAt:                 expireAtString,
		AbandonAt:                abandonAtString,
		TouchedAt:                touchedAtString,
		SessionActivityId:        &sessionActivityID,
		Status:                   edge_client_service.SessionResponseStatusEnum("active"),
		SessionInactivityTimeout: 0,
		TokenIssuedAt:            &tokenIssuedAtString,
		ActiveOrganizationId:     &activeOrganizationID,
		Actor:                    &actor,
		TouchEventSentAt:         &touchEventSentAtString,
		TokenCreatedEventSentAt:  &tokenCreatedEventSentAtString,
		CreatedAt:                createdAtString,
		UpdatedAt:                updatedAtString,
	}

	// Copy it & test equality
	session := &Session{}
	err = EdgeClientServiceSessionResponseToSession(everythingSetSessionResponse, session)
	require.NoError(t, err)
	assert.Equal(t, &Session{
		ID:                       "sess_5fe2c9393087f743d69c1ec3b044e5c18103eddc92bc75e3232d6f582f169d82",
		InstanceID:               "ins_1lyWDZiobr600AKUeQDoSlrEmoM",
		ClientID:                 "client_66fc04fb4a09077d0a67f64add71d7b0237c1e34756d0223aa783caeaef143a9",
		UserID:                   "user_2cea06s4VcAoU7XZgfFcscdTjxB",
		ReplacementSessionID:     null.StringFrom(replacementSessionID),
		ExpireAt:                 expireAtTime,
		AbandonAt:                abandonAtTime,
		TouchedAt:                touchedAtTime,
		SessionActivityID:        null.StringFrom(sessionActivityID),
		Status:                   "active",
		SessionInactivityTimeout: 0,
		TokenIssuedAt:            null.TimeFrom(tokenIssuedAtTime),
		ActiveOrganizationID:     null.StringFrom(activeOrganizationID),
		Actor:                    null.JSONFrom(actorBytes),
		TouchEventSentAt:         null.TimeFrom(touchEventSentAtTime),
		TokenCreatedEventSentAt:  null.TimeFrom(tokenCreatedEventSentAtTime),
		CreatedAt:                createdAtTime,
		UpdatedAt:                updatedAtTime,
	}, session)

	// Test the case where everything that can be nulled out is set as nil
	allOptionalsAsNilResponse := edge_client_service.SessionResponse{
		Id:                       "sess_5fe2c9393087f743d69c1ec3b044e5c18103eddc92bc75e3232d6f582f169d82",
		InstanceId:               "ins_1lyWDZiobr600AKUeQDoSlrEmoM",
		ClientId:                 "client_66fc04fb4a09077d0a67f64add71d7b0237c1e34756d0223aa783caeaef143a9",
		UserId:                   "user_2cea06s4VcAoU7XZgfFcscdTjxB",
		ReplacementSessionId:     nil,
		ExpireAt:                 expireAtString,
		AbandonAt:                abandonAtString,
		TouchedAt:                touchedAtString,
		SessionActivityId:        nil,
		Status:                   edge_client_service.SessionResponseStatusEnum("active"),
		SessionInactivityTimeout: 0,
		TokenIssuedAt:            nil,
		ActiveOrganizationId:     nil,
		Actor:                    nil,
		TouchEventSentAt:         nil,
		TokenCreatedEventSentAt:  nil,
		CreatedAt:                createdAtString,
		UpdatedAt:                updatedAtString,
	}
	nilsSession := &Session{}
	err = EdgeClientServiceSessionResponseToSession(allOptionalsAsNilResponse, nilsSession)
	require.NoError(t, err)
	assert.Equal(t, &Session{
		ID:                       "sess_5fe2c9393087f743d69c1ec3b044e5c18103eddc92bc75e3232d6f582f169d82",
		InstanceID:               "ins_1lyWDZiobr600AKUeQDoSlrEmoM",
		ClientID:                 "client_66fc04fb4a09077d0a67f64add71d7b0237c1e34756d0223aa783caeaef143a9",
		UserID:                   "user_2cea06s4VcAoU7XZgfFcscdTjxB",
		ExpireAt:                 expireAtTime,
		AbandonAt:                abandonAtTime,
		TouchedAt:                touchedAtTime,
		Status:                   "active",
		SessionInactivityTimeout: 0,
		CreatedAt:                createdAtTime,
		UpdatedAt:                updatedAtTime,
	}, nilsSession)
}
