package clients

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/pagination"
	"clerk/pkg/cenv"
	"clerk/pkg/ctx/environment"
	"clerk/utils/database"
)

// ReadAllPaginated calls ReadAll and includes the total count along with the
// results.
func (s *Service) ReadAllPaginated(ctx context.Context, pagination pagination.Params) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	var list []*serialize.ClientResponseServerAPI
	var totalCount int64
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var apiErr apierror.Error
		list, apiErr = s.readAll(ctx, tx, pagination)
		if apiErr != nil {
			return true, apiErr
		}
		var err error
		totalCount, err = s.clientRepo.CountByInstance(ctx, tx, env.Instance.ID)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		if apiErr, isAPIError := apierror.As(txErr); isAPIError {
			return nil, apiErr
		}
		return nil, apierror.Unexpected(txErr)
	}

	data := make([]any, len(list))
	for i, client := range list {
		data[i] = client
	}
	return serialize.Paginated(data, totalCount), nil
}

// ReadAll returns all clients for given instance.
func (s *Service) ReadAll(ctx context.Context, pagination pagination.Params) ([]*serialize.ClientResponseServerAPI, apierror.Error) {
	return s.readAll(ctx, s.db, pagination)
}

func (s *Service) readAll(ctx context.Context, exec database.Executor, pagination pagination.Params) ([]*serialize.ClientResponseServerAPI, apierror.Error) {
	if deprecatedEndpoint(ctx, cenv.ClerkSkipBAPIEndpointListClientsDeprecationApplicationIDs) {
		return nil, apierror.BAPIEndpointDeprecated("This endpoint is deprecated and will be removed in future versions.")
	}
	env := environment.FromContext(ctx)

	clients, err := s.clientRepo.FindAllWithModifiers(ctx, exec, env.Instance.ID, pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	clientsWithSessions, convErr := s.ConvertClientsForBAPI(ctx, clients)
	if err != nil {
		return nil, apierror.Unexpected(convErr)
	}

	clientResponses := make([]*serialize.ClientResponseServerAPI, len(clients))
	for i, clientWithSessions := range clientsWithSessions {
		clientResponses[i] = serialize.ClientToServerAPI(s.clock, clientWithSessions)
	}

	return clientResponses, nil
}

func deprecatedEndpoint(ctx context.Context, allowlistKey string) bool {
	if !cenv.IsSet(allowlistKey) {
		return false
	}
	env := environment.FromContext(ctx)
	return !cenv.ResourceHasAccess(allowlistKey, env.Instance.ApplicationID)
}
