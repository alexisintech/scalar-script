package sessions

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/pagination"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/repository"
	"clerk/utils/database"
)

type readAllParams struct {
	clientID *string
	userID   *string
	status   *string
}

func (r readAllParams) validate(ctx context.Context) apierror.Error {
	if deprecatedEndpoint(ctx, cenv.ClerkSkipBAPIEndpointListSessionsWithoutClientOrUserDeprecationApplicationIDs) {
		if r.userID == nil && r.clientID == nil {
			return apierror.FormAtLeastOneOptionalParameterMissing("client_id", "user_id")
		}
	}
	if r.status != nil && !constants.SessionStatuses.Contains(*r.status) {
		return apierror.FormInvalidParameterValueWithAllowed("status", *r.status, constants.SessionStatuses.Array())
	}

	return nil
}

func (r readAllParams) convertToSessionMods() repository.SessionsFindAllModifiers {
	return repository.SessionsFindAllModifiers{
		ClientID: r.clientID,
		UserID:   r.userID,
		Status:   r.status,
	}
}

// ReadAllPaginated calls ReadAll to get a list of sessions based on the passed parameters
// and includes the total count of sessions for the instance in the response.
func (s *Service) ReadAllPaginated(ctx context.Context, readParams readAllParams, pagination pagination.Params) (*serialize.PaginatedResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	var list []*serialize.SessionServerResponse
	var totalCount int64
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		var apiErr apierror.Error
		list, apiErr = s.readAll(ctx, tx, readParams, pagination)
		if apiErr != nil {
			return true, apiErr
		}
		var err error
		totalCount, err = s.sessionsRepo.CountByInstanceWithModifiers(
			ctx,
			tx,
			env.Instance.ID,
			readParams.convertToSessionMods(),
		)
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
	for i, sess := range list {
		data[i] = sess
	}
	return serialize.Paginated(data, totalCount), nil
}

// ReadAll returns all sessions for the given instance.
func (s *Service) ReadAll(ctx context.Context, readParams readAllParams, pagination pagination.Params) ([]*serialize.SessionServerResponse, apierror.Error) {
	return s.readAll(ctx, s.db, readParams, pagination)
}

func (s *Service) readAll(ctx context.Context, exec database.Executor, readParams readAllParams, pagination pagination.Params) ([]*serialize.SessionServerResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := readParams.validate(ctx); apiErr != nil {
		return nil, apiErr
	}

	findAllParams := readParams.convertToSessionMods()

	sessions, err := s.sessionsRepo.FindAllWithModifiers(ctx, exec, env.Instance.ID, findAllParams, pagination)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	responses := make([]*serialize.SessionServerResponse, len(sessions))
	for i, session := range sessions {
		responses[i] = serialize.SessionToServerAPI(s.clock, session)
	}

	return responses, nil
}
