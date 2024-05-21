package applications

import (
	"context"
	"fmt"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/domains"
	"clerk/api/shared/edgecache"
	"clerk/api/shared/edgereplication"
	"clerk/model"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/externalapis/svix"
	"clerk/pkg/jobs"
	"clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Deleter struct {
	clock     clockwork.Clock
	gueClient *gue.Client

	// services
	domainService          *domains.Service
	edgeReplicationService *edgereplication.Service

	// repositories
	applicationRepo  *repository.Applications
	domainRepo       *repository.Domain
	instanceRepo     *repository.Instances
	subscriptionRepo *repository.Subscriptions
}

func NewDeleter(deps clerk.Deps) *Deleter {
	return &Deleter{
		clock:                  deps.Clock(),
		gueClient:              deps.GueClient(),
		domainService:          domains.NewService(deps),
		edgeReplicationService: edgereplication.NewService(deps.GueClient(), cenv.GetBool(cenv.FlagReplicateInstanceToEdgeJobsEnabled)),
		applicationRepo:        repository.NewApplications(),
		domainRepo:             repository.NewDomain(),
		instanceRepo:           repository.NewInstances(),
		subscriptionRepo:       repository.NewSubscriptions(),
	}
}

func (d *Deleter) ScheduleSoftDeleteOfOwnedApplications(
	ctx context.Context,
	tx database.Tx,
	ownerID string,
	ownerType string,
) error {
	var applicationsToDelete []*model.Application
	var err error
	switch ownerType {
	case constants.UserResource:
		applicationsToDelete, err = d.applicationRepo.FindAllByUser(ctx, tx, ownerID)
	case constants.OrganizationResource:
		applicationsToDelete, err = d.applicationRepo.FindAllByOrganization(ctx, tx, ownerID)
	default:
		err = fmt.Errorf("unable to find owned applications, unsupported owner type %s", ownerType)
	}
	if err != nil {
		return err
	}

	applicationIDsToDelete := make([]string, 0, len(applicationsToDelete))
	for _, applicationToDelete := range applicationsToDelete {
		if applicationToDelete.Type == string(constants.RTSystem) {
			continue
		}
		applicationIDsToDelete = append(applicationIDsToDelete, applicationToDelete.ID)
	}
	if len(applicationIDsToDelete) == 0 {
		return nil
	}

	return jobs.SoftDeleteApplications(ctx, d.gueClient, jobs.SoftDeleteApplicationsArgs{
		ApplicationIDs: applicationIDsToDelete,
		HardDeleteAt:   d.clock.Now().UTC().Add(cenv.GetDurationInSeconds(cenv.HardDeleteApplicationAfterOwnerDeletionInSeconds)),
	}, jobs.WithTx(tx))
}

// SoftDelete marks the given application as deleted, without in fact deleting its
// data permanently. This means that the application is still operational.
// One thing that it does though is to cancel its subscription.
func (d *Deleter) SoftDelete(
	ctx context.Context,
	txEmitter database.TxEmitter,
	appID string,
	hardDeleteAt time.Time,
	paymentProvider billing.PaymentProvider,
) apierror.Error {
	app, err := d.applicationRepo.QueryByID(ctx, txEmitter, appID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if app == nil {
		return nil
	}

	if app.Type == string(constants.RTSystem) {
		return apierror.NotAuthorizedToDeleteSystemApplication(app.ID)
	}

	domains, err := d.domainRepo.FindAllByAppID(ctx, txEmitter, app.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	for _, domain := range domains {
		err = edgecache.PurgeAllFapiByDomain(ctx, d.gueClient, txEmitter, domain)
		if err != nil {
			return apierror.Unexpected(err)
		}
		err = edgecache.PurgeJWKS(ctx, d.gueClient, txEmitter, domain)
		if err != nil {
			return apierror.Unexpected(err)
		}
	}

	app.HardDeleteAt = null.TimeFrom(hardDeleteAt)
	err = d.applicationRepo.UpdateHardDeleteAt(ctx, txEmitter, app)
	if err != nil {
		return apierror.Unexpected(err)
	}

	subscription, err := d.subscriptionRepo.FindByResourceID(ctx, txEmitter, app.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if err := d.cancelSubscription(ctx, subscription, paymentProvider); err != nil {
		sentry.CaptureException(ctx, err)
	}

	err = jobs.HardDeleteApplication(ctx, d.gueClient, jobs.HardDeleteApplicationArgs{
		ApplicationID: appID,
	}, jobs.WithTx(txEmitter), jobs.WithRunAt(&hardDeleteAt))
	if err != nil {
		return apierror.Unexpected(err)
	}
	return nil
}

// HardDelete deletes the given application and all of its associated data permanently
func (d *Deleter) HardDelete(
	ctx context.Context,
	txEmitter database.TxEmitter,
	appID string,
	paymentProvider billing.PaymentProvider,
	svixClient *svix.Client,
) apierror.Error {
	app, err := d.applicationRepo.QueryByID(ctx, txEmitter, appID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if app == nil {
		return nil
	}

	if app.Type == string(constants.RTSystem) {
		return apierror.NotAuthorizedToDeleteSystemApplication(app.ID)
	}

	subscription, err := d.subscriptionRepo.FindByResourceID(ctx, txEmitter, app.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	domains, err := d.domainRepo.FindAllByAppID(ctx, txEmitter, app.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	instances, err := d.instanceRepo.FindAllByApplication(ctx, txEmitter, app.ID)
	if err != nil {
		return apierror.Unexpected(err)
	}

	for _, domain := range domains {
		if err := d.domainService.Delete(ctx, txEmitter, domain); err != nil {
			return apierror.Unexpected(err)
		}
	}

	if app.LogoPublicURL.Valid {
		err = jobs.CleanupImage(
			ctx,
			d.gueClient,
			jobs.CleanupImageArgs{
				PublicURL: app.LogoPublicURL.String,
			},
			jobs.WithTx(txEmitter),
		)
		if err != nil {
			return apierror.Unexpected(err)
		}
	}

	if app.FaviconPublicURL.Valid {
		err = jobs.CleanupImage(
			ctx,
			d.gueClient,
			jobs.CleanupImageArgs{
				PublicURL: app.FaviconPublicURL.String,
			},
			jobs.WithTx(txEmitter),
		)
		if err != nil {
			return apierror.Unexpected(err)
		}
	}

	for _, instance := range instances {
		if instance.IsSvixEnabled() {
			if err := svixClient.Delete(instance.SvixAppID.String); err != nil {
				// do not fail, just report the error to sentry
				sentry.CaptureException(ctx,
					fmt.Errorf("application: failed to delete Svix app %s for instance %s in application %s: %w",
						instance.SvixAppID.String, instance.ID, app.ID, err))
			}
		}

		err = d.instanceRepo.DeleteByID(ctx, txEmitter, instance.ID)
		if err != nil {
			// do not fail, no need to roll back the transaction given that the instance is going
			// to be unusable at this point...instead, report the error to look at it manually
			sentry.CaptureException(ctx,
				fmt.Errorf("application: failed to delete instance %s in application %s: %w",
					instance.ID, app.ID, err))
		}
	}

	if err := d.cancelSubscription(ctx, subscription, paymentProvider); err != nil {
		sentry.CaptureException(ctx, err)
	}

	if err = d.applicationRepo.DeleteByID(ctx, txEmitter, app.ID); err != nil {
		return apierror.Unexpected(err)
	}

	if err = d.subscriptionRepo.DeleteByResourceID(ctx, txEmitter, app.ID); err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}

func (d *Deleter) cancelSubscription(ctx context.Context, subscription *model.Subscription, paymentProvider billing.PaymentProvider) error {
	if !subscription.StripeSubscriptionID.Valid {
		return nil
	}

	return paymentProvider.CancelSubscription(ctx, subscription.StripeSubscriptionID.String, billing.CancelSubscriptionParams{
		InvoiceNow: true,
		Prorate:    false,
	})
}
