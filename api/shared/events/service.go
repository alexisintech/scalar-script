package events

import (
	"context"
	"fmt"
	"time"

	"clerk/model"
	"clerk/pkg/cache"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/events"
	"clerk/pkg/jobs"
	"clerk/pkg/rand"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"cloud.google.com/go/pubsub"
	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
)

type Service struct {
	clock clockwork.Clock

	cache             cache.Cache
	gueClient         *gue.Client
	pubsubEventsTopic *pubsub.Topic
	organizationRepo  *repository.Organization
	userRepo          *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:             deps.Clock(),
		cache:             deps.Cache(),
		gueClient:         deps.GueClient(),
		pubsubEventsTopic: deps.PubsubEventsTopic(),
		organizationRepo:  repository.NewOrganization(),
		userRepo:          repository.NewUsers(),
	}
}

type svixEvent struct {
	Object string      `json:"object"`
	Type   string      `json:"type"`
	Data   interface{} `json:"data"`
}

type sendEventParams struct {
	Instance  *model.Instance
	EventType events.EventType
	Payload   interface{}

	UserID           *string
	OrganizationID   *string
	SAMLConnectionID *string
	ActorID          *string
}

func (s *Service) sendEvent(
	ctx context.Context,
	exec database.Executor,
	params sendEventParams) error {
	if maintenance.FromContext(ctx) {
		// We don't register events during maintenance.
		// We have the flexibility to do that because the only events that can be
		// generated during maintenance are internal ones that we're ok if we miss them.
		return nil
	}
	if params.EventType.OncePerDay {
		yesterday := s.clock.Now().UTC().Add(-24 * time.Hour)

		if params.EventType.UserActivity && params.UserID != nil {
			user, err := s.userRepo.QueryByID(ctx, exec, *params.UserID)
			if err != nil {
				return err
			}
			if user == nil {
				// user no longer exists, probably deleted in a concurrent request,
				// we don't register activity, to avoid double-registering in cases
				// we've already processed this user
				return nil
			}
			if user.LastActiveAt.Valid && user.LastActiveAt.Time.After(yesterday) {
				return nil
			}
		}
		if params.EventType.OrganizationActivity && params.OrganizationID != nil {
			organization, err := s.organizationRepo.QueryByID(ctx, exec, *params.OrganizationID)
			if err != nil {
				return err
			}
			if organization == nil {
				// same reasoning as with the user...the organization was probably deleted
				// in a concurrent request, so we shouldn't register activity to avoid
				// double processing this organization
				return nil
			}
			if organization.LastActiveAt.Valid && organization.LastActiveAt.Time.After(yesterday) {
				return nil
			}
		}
		if params.EventType.InstanceActivity {
			cacheKey := "event:" + params.EventType.Name + ":instance:" + params.Instance.ID
			exists, err := s.cache.Exists(ctx, cacheKey)
			if err != nil {
				return err
			}
			if exists {
				return nil
			}
			err = s.cache.Set(ctx, cacheKey, true, 24*time.Hour) // set the value to anything -- if the Cache Key exists and is unexpired, events will no longer be sent.
			if err != nil {
				return err
			}
		}
	}
	// log one request per day to Redis to ensure we are not sending overloading requests

	eventTime := s.clock.Now().UTC()
	eventID, err := rand.InternalClerkEventID(eventTime)
	if err != nil {
		return err
	}

	if params.EventType.UserActivity || params.EventType.OrganizationActivity || params.EventType.SAMLActivity {
		err := s.registerActivity(ctx, exec, params)
		if err != nil {
			return err
		}
	}

	err = jobs.PublishEvent(ctx, s.gueClient, jobs.PublishEventArgs{
		ID:               eventID,
		InstanceID:       params.Instance.ID,
		OrganizationID:   params.OrganizationID,
		UserID:           params.UserID,
		SAMLConnectionID: params.SAMLConnectionID,
		ActorID:          params.ActorID,
		EventType:        params.EventType.Name,
		Payload:          params.Payload,
		Time:             eventTime,
	}, jobs.WithTxIfApplicable(exec))
	if err != nil {
		return err
	}

	if params.EventType.Internal {
		return nil
	}

	return s.sendEventToWebhook(ctx, exec, eventID, params.Instance, params.EventType, params.Payload)
}

func (s *Service) registerActivity(ctx context.Context, exec database.Executor, params sendEventParams) error {
	if params.ActorID != nil {
		return nil
	}

	args := jobs.RegisterDailyActivityArgs{
		InstanceID: params.Instance.ID,
		Day:        s.clock.Now().UTC(),
	}

	if params.EventType.UserActivity && params.UserID != nil {
		args.ResourceID = *params.UserID
		args.ResourceType = constants.UserAggregationType
	} else if params.EventType.OrganizationActivity && params.OrganizationID != nil {
		args.ResourceID = *params.OrganizationID
		args.ResourceType = constants.OrganizationAggregationType
	} else if params.EventType.SAMLActivity && params.SAMLConnectionID != nil {
		args.ResourceID = *params.SAMLConnectionID
		args.ResourceType = constants.SAMLActivationAggregationType
	} else {
		return fmt.Errorf("events/registerActivity: unable to register activity, missing info in %+v", params)
	}

	var jobOpts []jobs.JobOptionFunc
	if tx, isTx := exec.(database.Tx); isTx {
		jobOpts = append(jobOpts, jobs.WithTx(tx))
	}
	err := jobs.RegisterDailyActivity(ctx, s.gueClient, args, jobOpts...)
	if err != nil {
		return fmt.Errorf("events/registerActivity: enqueuing job %+v: %w", args, err)
	}

	return nil
}

func (s *Service) sendEventToWebhook(
	ctx context.Context,
	exec database.Executor,
	eventID string,
	instance *model.Instance,
	eventType events.EventType,
	payload interface{},
) error {
	if !instance.ShouldSendWebhook() {
		return nil
	}

	event := jobs.WebhookEventArgs{
		InstanceID: instance.ID,
		EventID:    eventID,
		EventType:  eventType,
		Payload: &svixEvent{
			Object: "event",
			Type:   eventType.Name,
			Data:   payload,
		},
	}

	if tx, isTx := exec.(database.Tx); isTx {
		err := jobs.DispatchWebhookEvent(ctx, s.gueClient, event, jobs.WithTx(tx))
		if err != nil {
			return fmt.Errorf("events/send: enqueuing job %+v in context of transaction: %w", event, err)
		}
	} else {
		err := jobs.DispatchWebhookEvent(ctx, s.gueClient, event)
		if err != nil {
			return fmt.Errorf("events/send: enqueuing job %+v: %w", event, err)
		}
	}
	return nil
}
