package edgecache

import (
	"context"
	"strings"

	"github.com/vgarvardt/gue/v2"

	"clerk/api/shared/edgecache/resource"
	"clerk/model"
	"clerk/pkg/cenv"
	errors "clerk/pkg/clerkerrors"
	"clerk/pkg/constants"
	"clerk/pkg/jobs"
	"clerk/repository"
	"clerk/utils/database"
)

// PurgeAllFapiByDomain schedules a job to purge all FAPI cache of domain.
func PurgeAllFapiByDomain(ctx context.Context, gc *gue.Client, exec database.Executor, domain *model.Domain) error {
	tag := domain.FapiHost()
	zone := cenv.Get(cenv.CloudflareFAPIZoneID)

	if tx, ok := exec.(database.Tx); ok {
		return SchedulePurgeByTag(ctx, gc, zone, tag, jobs.WithTx(tx))
	}
	return SchedulePurgeByTag(ctx, gc, zone, tag)
}

// PurgeJWKS schedules a job to purge all FAPI and BAPI JWKS cache of domain.
func PurgeJWKS(ctx context.Context, gueClient *gue.Client, exec database.Executor, domain *model.Domain) error {
	resources := []Resource{resource.NewFapiJWKS(domain)}

	keys, err := repository.NewInstanceKeys().FindAllByInstance(ctx, exec, domain.InstanceID)
	if err != nil {
		return errors.WithStacktrace("edgecache: JWKS purge: %w", err)
	}
	for _, k := range keys {
		r, err := resource.NewBapiJWKS(k.Secret)
		if err != nil {
			return err
		}
		resources = append(resources, r)
	}

	if tx, ok := exec.(database.Tx); ok {
		return SchedulePurgeByResources(ctx, gueClient, resources, jobs.WithTx(tx))
	}
	return SchedulePurgeByResources(ctx, gueClient, resources)
}

// PurgeFapiEnvironment enqueues a job to purge FAPI's `/v1/environment` cache
// of each of the instance's domains.
func PurgeFapiEnvironment(ctx context.Context, gueClient *gue.Client, exec database.Executor, instanceID string) error {
	if !strings.HasPrefix(instanceID, constants.IDPInstance) {
		return errors.WithStacktrace("edgecache: FAPI environment purge: invalid instance ID (%s)", instanceID)
	}

	domains, err := repository.NewDomain().FindAllByInstanceID(ctx, exec, instanceID)
	if err != nil {
		return errors.WithStacktrace("edgecache: FAPI environment purge: %w", err)
	}

	return purgeFapiEnvironmentByDomains(ctx, gueClient, exec, domains)
}

// PurgeFapiEnvironmentByApplication enqueues a job to purge FAPI's
// `/v1/environment` cache of each of the application's domains.
func PurgeFapiEnvironmentByApplication(ctx context.Context, gueClient *gue.Client, exec database.Executor, app *model.Application) error {
	domains, err := repository.NewDomain().FindAllByAppID(ctx, exec, app.ID)
	if err != nil {
		return errors.WithStacktrace("edgecache: FAPI environment purge: %w", err)
	}

	return purgeFapiEnvironmentByDomains(ctx, gueClient, exec, domains)
}

func purgeFapiEnvironmentByDomains(ctx context.Context, gueClient *gue.Client, exec database.Executor, domains []*model.Domain) error {
	if len(domains) == 0 {
		return nil
	}

	resources := make([]Resource, len(domains))
	for i, d := range domains {
		resources[i] = resource.NewFapiEnvironment(d)
	}

	return SchedulePurgeByResources(ctx, gueClient, resources, jobs.WithTxIfApplicable(exec))
}
