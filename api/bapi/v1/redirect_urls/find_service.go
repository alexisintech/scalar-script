package redirect_urls

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"
)

// ReadAllPaginated calls ReadAll but returns the results in a
// serialize.PaginatedResponse format.
// Does not support actual pagination.
func (s *Service) ReadAllPaginated(ctx context.Context) (*serialize.PaginatedResponse, apierror.Error) {
	list, apiErr := s.ReadAll(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	totalCount := len(list)
	data := make([]any, totalCount)
	for i, redirectURL := range list {
		data[i] = redirectURL
	}
	return serialize.Paginated(data, int64(totalCount)), nil
}

func (s *Service) ReadAll(ctx context.Context) ([]*serialize.RedirectURLResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	redirectUrls, err := s.redirectUrlsRepo.FindAllByInstance(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	results := make([]*serialize.RedirectURLResponse, len(redirectUrls))
	for i, redirectURL := range redirectUrls {
		results[i] = serialize.RedirectURL(redirectURL)
	}

	return results, nil
}

func (s *Service) Read(ctx context.Context, ID string) (*serialize.RedirectURLResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	redirectURL, err := s.redirectUrlsRepo.QueryByIDAndInstance(ctx, s.db, ID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if redirectURL == nil {
		return nil, apierror.RedirectURLNotFound("id", ID)
	}

	return serialize.RedirectURL(redirectURL), nil
}
