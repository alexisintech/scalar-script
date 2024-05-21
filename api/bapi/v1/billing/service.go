package billing

import (
	"context"

	"clerk/api/apierror"
	"clerk/pkg/billing"
	"clerk/pkg/cenv"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	db               database.Database
	billingConnector billing.Connector
	instanceRepo     *repository.Instances

	eventHandlers map[string]func(context.Context, billing.Event) apierror.Error
}

func NewService(deps clerk.Deps, billingConnector billing.Connector) *Service {
	s := &Service{
		db:               deps.DB(),
		billingConnector: billingConnector,
		instanceRepo:     repository.NewInstances(),
		eventHandlers:    make(map[string]func(context.Context, billing.Event) apierror.Error),
	}
	s.registerEventHandlers()
	return s
}

func (s *Service) HandleStripeEvent(ctx context.Context, signature string, payload []byte) apierror.Error {
	event, err := s.billingConnector.ParseEvent(payload, signature, cenv.Get(cenv.BillingStripeWebhookSecret))
	if err != nil {
		return apierror.InvalidRequestBody(err)
	}

	if handler, ok := s.eventHandlers[event.Type]; ok {
		return handler(ctx, event)
	}

	return nil
}

func (s *Service) registerEventHandlers() {
	s.eventHandlers["billing_portal.configuration.created"] = s.handleBillingPortalConfigurationCreated
}

func (s *Service) handleBillingPortalConfigurationCreated(ctx context.Context, event billing.Event) apierror.Error {
	ins, err := s.instanceRepo.QueryByExternalBillingAccountID(ctx, s.db, event.AccountID)
	if err != nil {
		return apierror.Unexpected(err)
	}
	if ins == nil {
		log.Debug(ctx, "Instance not found for account ID %s", event.AccountID)
		return nil
	}

	ins.BillingPortalEnabled = null.BoolFrom(true)
	if err := s.instanceRepo.UpdateBillingPortalEnabled(ctx, s.db, ins); err != nil {
		return apierror.Unexpected(err)
	}

	return nil
}
