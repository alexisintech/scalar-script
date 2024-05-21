package environment

import (
	"context"

	"clerk/api/apierror"
	"clerk/api/shared/environment"
	"clerk/api/shared/sentryenv"
	ctxenv "clerk/pkg/ctx/environment"
	"clerk/pkg/ctxkeys"
	"clerk/repository"
	"clerk/utils/database"
	"clerk/utils/log"
)

type Service struct {
	db database.Database

	// services
	environmentService *environment.Service

	// repositories
	instanceKeysRepo *repository.InstanceKeys
}

func NewService(db database.Database) *Service {
	return &Service{
		db:                 db,
		environmentService: environment.NewService(),
		instanceKeysRepo:   repository.NewInstanceKeys(),
	}
}

// SetEnvironmentFromKey loads the instance and its dependents (i.e. domain, display config, auth config, etc)
// into the context
func (s *Service) SetEnvironmentFromKey(ctx context.Context, secretKey string) (context.Context, apierror.Error) {
	key, err := s.instanceKeysRepo.QueryBySecretWithInstanceAndApplication(ctx, s.db, secretKey)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	} else if key == nil {
		return ctx, apierror.InvalidClerkSecretKey()
	}

	env, err := s.environmentService.Load(ctx, s.db, key.InstanceID)
	if err != nil {
		return ctx, apierror.Unexpected(err)
	}

	log.AddToLogLine(ctx, log.InstanceID, env.Instance.ID)
	log.AddToLogLine(ctx, log.EnvironmentType, env.Instance.EnvironmentType)
	log.AddToLogLine(ctx, log.DomainName, env.Domain.Name)

	sentryenv.EnrichScope(ctx, env)

	ctx = context.WithValue(ctx, ctxkeys.InstanceKey, key)

	return ctxenv.NewContext(ctx, env), nil
}
