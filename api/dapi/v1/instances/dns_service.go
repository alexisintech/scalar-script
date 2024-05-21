package instances

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

type DNSService struct {
	clock     clockwork.Clock
	db        database.Database
	gueClient *gue.Client

	// repositories
	dnsChecksRepo *repository.DNSChecks
	instanceRepo  *repository.Instances
}

func NewDNSService(clock clockwork.Clock, db database.Database, gueClient *gue.Client) *DNSService {
	return &DNSService{
		clock:         clock,
		db:            db,
		gueClient:     gueClient,
		dnsChecksRepo: repository.NewDNSChecks(),
		instanceRepo:  repository.NewInstances(),
	}
}

// Retry enqueues another job to perform a DNS check
func (s *DNSService) Retry(ctx context.Context, instanceID string) apierror.Error {
	instance, err := s.instanceRepo.QueryByID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if instance == nil || !instance.IsProduction() {
		return apierror.InstanceNotFound(instanceID)
	}

	check, err := s.dnsChecksRepo.FindByInstanceID(ctx, s.db, instanceID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if check.JobInflight {
		return apierror.TooManyRequests()
	}
	nextRunAt := check.LastRunAt.Time.Add(cenv.GetDurationInSeconds(cenv.DNSEntryCacheExpiryInSeconds))
	if nextRunAt.After(s.clock.Now().UTC()) { // Retried too early
		return apierror.TooManyRequests()
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err = jobs.CheckDNSRecords(ctx, s.gueClient,
			jobs.CheckDNSRecordsArgs{
				InstanceID: check.InstanceID,
			}, jobs.WithTx(tx))
		if err != nil {
			return true, err
		}

		check.JobInflight = true
		err = s.dnsChecksRepo.Update(ctx, tx, check)
		if err != nil {
			return true, err
		}
		return false, nil
	})
	if txErr != nil {
		return apierror.Unexpected(txErr)
	}

	return nil
}
