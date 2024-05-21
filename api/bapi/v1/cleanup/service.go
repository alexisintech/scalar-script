package cleanup

import (
	"context"

	"clerk/api/apierror"
	"clerk/pkg/cenv"
	"clerk/pkg/jobs"
	"clerk/repository"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	clock           clockwork.Clock
	db              database.Database
	gueClient       *gue.Client
	applicationRepo *repository.Applications
}

func NewService(clock clockwork.Clock, db database.Database, gueClient *gue.Client) *Service {
	return &Service{
		clock:           clock,
		db:              db,
		gueClient:       gueClient,
		applicationRepo: repository.NewApplications(),
	}
}

const (
	defaultDeadSessionsLimit = 1000
)

func (s *Service) DeadSessionsJob(ctx context.Context, limit int) apierror.Error {
	if limit == 0 {
		limit = defaultDeadSessionsLimit
	}
	err := jobs.CleanupDeadSessions(ctx, s.gueClient, jobs.CleanupDeadSessionsArgs{
		Limit: limit,
	})
	if err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}

const (
	defaultOrphanApplicationsLimit = 10
)

func (s *Service) OrphanApplications(ctx context.Context, limit int) apierror.Error {
	if limit == 0 {
		limit = defaultOrphanApplicationsLimit
	}
	applicationsToDelete, err := s.applicationRepo.FindAllOrphanNonSystemApplications(ctx, s.db, limit)
	if err != nil {
		return apierror.Unexpected(err)
	}
	applicationIDsToDelete := make([]string, len(applicationsToDelete))
	for i, applicationToDelete := range applicationsToDelete {
		applicationIDsToDelete[i] = applicationToDelete.ID
	}
	if len(applicationIDsToDelete) > 0 {
		err = jobs.SoftDeleteApplications(ctx, s.gueClient, jobs.SoftDeleteApplicationsArgs{
			ApplicationIDs: applicationIDsToDelete,
			HardDeleteAt:   s.clock.Now().UTC().Add(cenv.GetDurationInSeconds(cenv.HardDeleteApplicationAfterOwnerDeletionInSeconds)),
		})
		if err != nil {
			return apierror.Unexpected(err)
		}
	}
	return nil
}

const (
	defaultOrphanOrganizationsLimit = 100
)

func (s *Service) OrphanOrganizations(ctx context.Context, limit int) apierror.Error {
	if limit == 0 {
		limit = defaultOrphanOrganizationsLimit
	}
	err := jobs.CleanupOrphanOrganizations(ctx, s.gueClient, jobs.CleanupOrphanOrganizationsArgs{
		Limit: limit,
	})
	if err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}

// ExpiredOAuthTokens deletes expired OAuth application tokens asynchronously.
func (s *Service) ExpiredOAuthTokens(ctx context.Context) apierror.Error {
	err := jobs.CleanupExpiredOAuthTokens(
		ctx,
		s.gueClient,
	)
	if err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}
