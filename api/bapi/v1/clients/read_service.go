package clients

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"
)

// Read returns the client that is loaded in the context.
// Make sure there is a call to SetClient before calling Read
func (s *Service) Read(ctx context.Context, clientID string) (*serialize.ClientResponseServerAPI, apierror.Error) {
	env := environment.FromContext(ctx)

	client, err := s.clientDataService.QueryClient(ctx, env.Instance.ID, clientID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if client == nil {
		return nil, apierror.ClientNotFound(clientID)
	}
	compatibleClient := client.ToClientModel()

	clientWithSessions, err := s.ConvertClientForBAPI(ctx, compatibleClient)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.ClientToServerAPI(s.clock, clientWithSessions), nil
}
