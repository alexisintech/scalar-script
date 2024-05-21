package edge_events

import (
	"context"
	"encoding/json"
	"fmt"

	sharedEvents "clerk/api/shared/events"
	"clerk/model"
	"clerk/pkg/events"
	"clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"cloud.google.com/go/pubsub"
)

type Service struct {
	db            database.Database
	instanceRepo  repository.Instances
	eventsService *sharedEvents.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:            deps.DB(),
		instanceRepo:  *repository.NewInstances(),
		eventsService: sharedEvents.NewService(deps),
	}
}

type MessageData struct {
	EventTypeName string         `json:"eventTypeName"`
	Session       *model.Session `json:"session"`
}

func (s *Service) HandleMessage(ctx context.Context, msg pubsub.Message) error {
	var data MessageData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		// our own message data is malformatted
		sentry.CaptureException(ctx, fmt.Errorf("edge_events/service: failed to unmarshall message: msg: %v, err: %w", msg, err))
		return nil
	}

	switch data.EventTypeName {
	case events.EventTypes.SessionTokenCreated.Name:
		if data.Session == nil {
			// our own message data is malformatted
			sentry.CaptureException(ctx, fmt.Errorf("edge_events/service: session is nil: data: %v", data))
			return nil
		}

		if err := s.edgeSessionTokenCreated(ctx, data.Session); err != nil {
			// Note: DeliveryAttempt will be `nil` if the associated pubsub subscription has no dead letter topic assigned.
			// We should assume that it has one. See https://cloud.google.com/pubsub/docs/handling-failures#track-delivery-attempts for details.
			attempt := msg.DeliveryAttempt
			// Allow up to 3 retries before ACK'ing and notifying Sentry
			if attempt == nil || *attempt >= 4 {
				sentry.CaptureException(ctx, fmt.Errorf("edge_events/service: failed to send event: data: %v; err: %w", data, err))
				return nil
			}
			return err
		}
		return nil
	default:
		sentry.CaptureException(ctx, fmt.Errorf("edge_events/service: unsupported eventType: %q", data.EventTypeName))
		return nil
	}
}

func (s *Service) edgeSessionTokenCreated(ctx context.Context, session *model.Session) error {
	instance, err := s.instanceRepo.FindByID(ctx, s.db, session.InstanceID)
	if err != nil {
		return err
	}
	return s.eventsService.SessionTokenCreated(ctx, s.db, instance, session)
}
