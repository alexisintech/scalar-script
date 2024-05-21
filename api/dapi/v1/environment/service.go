package environment

import (
	"context"

	"clerk/api/apierror"
	shenvironment "clerk/api/shared/environment"
	"clerk/pkg/ctx/environment"
	"clerk/utils/database"
)

type Service struct {
	db                 database.Database
	environmentService *shenvironment.Service
}

func NewService(db database.Database) *Service {
	return &Service{
		db:                 db,
		environmentService: shenvironment.NewService(),
	}
}

func (s *Service) LoadEnvFromInstance(ctx context.Context, instanceID string) (context.Context, apierror.Error) {
	env, err := s.environmentService.Load(ctx, s.db, instanceID)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	}
	return environment.NewContext(ctx, env), nil
}
