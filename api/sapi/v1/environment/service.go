package environment

import (
	"context"
	"database/sql"
	"errors"

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

func (s *Service) LoadToContext(ctx context.Context, instanceID string) (context.Context, apierror.Error) {
	env, err := s.environmentService.Load(ctx, s.db, instanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ctx, apierror.ResourceNotFound()
	}
	return environment.NewContext(ctx, env), nil
}
