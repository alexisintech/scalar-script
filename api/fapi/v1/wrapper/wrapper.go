package wrapper

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/clients"
	"clerk/model"
	"clerk/pkg/ctx/environment"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/utils/clerk"
	"clerk/utils/log"

	"github.com/jonboulle/clockwork"
)

// Wrapper provides operations for wrapping API responses with the given model.Client
type Wrapper struct {
	clock         clockwork.Clock
	clientService *clients.Service
}

// NewWrapper creates a new Wrapper
func NewWrapper(deps clerk.Deps) *Wrapper {
	return &Wrapper{
		clock:         deps.Clock(),
		clientService: clients.NewService(deps),
	}
}

// ErrorResponseClient depicts a JSON structure that accompanies certain errors of client API
type ErrorResponseClient struct {
	Client interface{} `json:"client"`
}

// WrapError creates an instance of ErrorResponseClient with the given model.Client and adds it to the given error
func (w *Wrapper) WrapError(ctx context.Context, err apierror.Error, client *model.Client) apierror.Error {
	if client == nil {
		return err
	}

	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)
	clientWithSessions, apiErr := w.clientService.ConvertToClientWithSessions(ctx, client, env)
	if apiErr != nil {
		return apiErr
	}

	r, respErr := serialize.ClientToClientAPI(ctx, w.clock, clientWithSessions, userSettings)
	if respErr != nil {
		log.Error(ctx, "Failed to convert client to error meta: %v", respErr)
		return nil
	}
	respClient := ErrorResponseClient{Client: r}
	return err.WithMeta(&respClient)
}

// ResponseWrapper depicts the JSON representation of a wrapped response with the client
type ResponseWrapper struct {
	Response interface{} `json:"response"`
	Client   interface{} `json:"client,omitempty"`
}

// WrapResponse returns a ResponseWrapper that contains the given response and the client
func (w *Wrapper) WrapResponse(ctx context.Context, response interface{}, client *model.Client) (*ResponseWrapper, apierror.Error) {
	env := environment.FromContext(ctx)
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	clientWithSessions, apiErr := w.clientService.ConvertToClientWithSessions(ctx, client, env)
	if apiErr != nil {
		return nil, apiErr
	}
	c, err := serialize.ClientToClientAPI(ctx, w.clock, clientWithSessions, userSettings)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return &ResponseWrapper{Response: response, Client: c}, nil
}
