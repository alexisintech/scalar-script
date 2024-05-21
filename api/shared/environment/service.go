package environment

import (
	"context"
	"fmt"

	"clerk/model"
	"clerk/repository"
	"clerk/utils/database"
)

type Service struct {
	envRepo *repository.Environment
}

func NewService() *Service {
	return &Service{
		envRepo: repository.NewEnvironment(),
	}
}

func (s *Service) LoadByDomain(ctx context.Context, exec database.Executor, domain *model.Domain) (*model.Env, error) {
	env, err := s.envRepo.FindByInstanceIDWithoutDomain(ctx, exec, domain.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("environment/load: by domain %s: %w", domain.ID, err)
	}
	env.Domain = domain
	return env, nil
}

func (s *Service) Load(ctx context.Context, exec database.Executor, instanceID string) (*model.Env, error) {
	env, err := s.envRepo.FindByInstanceIDWithDomain(ctx, exec, instanceID)
	if err != nil {
		return nil, fmt.Errorf("environment/load: by instance id %s: %w", instanceID, err)
	}
	return env, nil
}
