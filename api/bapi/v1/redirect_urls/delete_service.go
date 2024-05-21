package redirect_urls

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/pkg/ctx/environment"
)

func (s *Service) Delete(ctx context.Context, ID string) (*serialize.DeletedObjectResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	redirectURL, err := s.redirectUrlsRepo.QueryByIDAndInstance(ctx, s.db, ID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if redirectURL == nil {
		return nil, apierror.RedirectURLNotFound("id", ID)
	}

	err = s.redirectUrlsRepo.DeleteByIDAndInstance(ctx, s.db, ID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.DeletedObject(redirectURL.ID, serialize.RedirectURLName), nil
}
