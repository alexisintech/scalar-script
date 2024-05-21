package account_portal

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/serialize"
	ctxenv "clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/requestingdevbrowser"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	db database.Database

	// repositories
	accountPortalRepo *repository.AccountPortal
	devBrowserRepo    *repository.DevBrowser
}

func NewService(db database.Database) *Service {
	return &Service{
		db:                db,
		accountPortalRepo: repository.NewAccountPortal(),
		devBrowserRepo:    repository.NewDevBrowser(),
	}
}

// Read returns the account portal of an instance
func (s *Service) Read(ctx context.Context) (*serialize.AccountPortalFAPIResponse, apierror.Error) {
	env := ctxenv.FromContext(ctx)
	devBrowser := requestingdevbrowser.FromContext(ctx)

	accountPortal, err := s.accountPortalRepo.QueryByInstanceID(ctx, s.db, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if accountPortal == nil {
		return nil, apierror.ResourceNotFound()
	}

	return serialize.AccountPortalFAPI(
		accountPortal,
		env.Application,
		env.Instance,
		env.Domain,
		devBrowser,
	), nil
}
