package actor_tokens

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/model"
	"clerk/model/sqbmodel"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	cevents "clerk/pkg/events"
	sentryclerk "clerk/pkg/sentry"
	"clerk/pkg/ticket"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/sqlboiler/v4/types"
)

type Service struct {
	clock     clockwork.Clock
	db        database.Database
	validator *validator.Validate

	// services
	eventService *events.Service

	// repositories
	actorTokenRepo *repository.ActorToken
	userRepo       *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:          deps.Clock(),
		db:             deps.DB(),
		validator:      validator.New(),
		eventService:   events.NewService(deps),
		actorTokenRepo: repository.NewActorToken(),
		userRepo:       repository.NewUsers(),
	}
}

const (
	paramActor    = "actor"
	paramActorSub = "actor.sub"
	paramUserID   = "user_id"
)

type CreateParams struct {
	UserID                      string          `json:"user_id" form:"user_id" validate:"required"`
	Actor                       json.RawMessage `json:"actor" form:"actor" validate:"required"`
	ExpiresInSeconds            *int            `json:"expires_in_seconds" form:"expires_in_seconds" validate:"omitempty,numeric,gte=1"`
	SessionMaxDurationInSeconds *int            `json:"session_max_duration_in_seconds" form:"session_max_duration_in_seconds" validate:"omitempty,numeric,gte=1"`
}

func (p CreateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, params CreateParams) (*serialize.ActorTokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	apiErr := params.validate(s.validator)
	if apiErr != nil {
		return nil, apiErr
	}

	// make sure that the user exists in the given instance
	user, err := s.userRepo.QueryByIDAndInstance(ctx, s.db, params.UserID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if user == nil {
		return nil, apierror.FormMissingResource(paramUserID)
	}

	expiresInSeconds := constants.ExpiryTimeShort
	if params.ExpiresInSeconds != nil {
		expiresInSeconds = *params.ExpiresInSeconds
	}

	sessionMaxDurationInSeconds := constants.ExpiryTimeTransactional * 3
	if params.SessionMaxDurationInSeconds != nil {
		sessionMaxDurationInSeconds = *params.SessionMaxDurationInSeconds
	}

	actorToken := &model.ActorToken{
		ActorToken: &sqbmodel.ActorToken{
			UserID:                      params.UserID,
			Actor:                       types.JSON(params.Actor),
			Status:                      constants.StatusPending,
			InstanceID:                  env.Instance.ID,
			SessionMaxDurationInSeconds: sessionMaxDurationInSeconds,
		},
	}

	// make sure that the "actor.sub" claim is present
	actorID, err := actorToken.ActorID()
	if err != nil {
		return nil, apierror.FormInvalidParameterFormat(paramActor, "Must be valid JSON")
	}
	if actorID == "" {
		return nil, apierror.FormMissingParameter(paramActorSub)
	}

	var ticketToken string
	var ticketURL *url.URL
	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err := s.actorTokenRepo.Insert(ctx, tx, actorToken)
		if err != nil {
			return true, err
		}

		ticketToken, err = ticket.Generate(
			ticket.Claims{
				InstanceID:       env.Instance.ID,
				SourceType:       constants.OSTActorToken,
				SourceID:         actorToken.ID,
				ExpiresInSeconds: &expiresInSeconds,
			},
			env.Instance,
			s.clock,
		)
		if err != nil {
			return true, err
		}

		fullURL, err := url.JoinPath(env.Domain.FapiURL(), "/v1/tickets/accept")
		if err != nil {
			return true, err
		}

		ticketURL, err = url.Parse(fullURL)
		if err != nil {
			return true, err
		}
		query := ticketURL.Query()
		query.Add("ticket", ticketToken)
		ticketURL.RawQuery = query.Encode()

		return false, nil
	})
	if txErr != nil {
		return nil, apierror.Unexpected(txErr)
	}

	response := serialize.ActorToken(actorToken, ticketURL, ticketToken)

	err = s.eventService.ActorTokenIssued(ctx, s.db, env.Instance, response)
	if err != nil {
		sentryclerk.CaptureException(ctx, fmt.Errorf("actor_tokens/create: error sending %s event for %v: %w",
			cevents.EventTypes.ActorTokenIssued, response, err))
	}

	return response, nil
}

func (s *Service) Revoke(ctx context.Context, actorTokenID string) (*serialize.ActorTokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	actorToken, err := s.actorTokenRepo.QueryByIDAndInstance(ctx, s.db, actorTokenID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	if actorToken == nil {
		return nil, apierror.ResourceNotFound()
	}

	if actorToken.Status != constants.StatusPending {
		return nil, apierror.ActorTokenCannotBeRevoked(actorToken.Status)
	}

	actorToken.Status = constants.StatusRevoked
	if err := s.actorTokenRepo.UpdateStatus(ctx, s.db, actorToken); err != nil {
		return nil, apierror.Unexpected(err)
	}

	return serialize.ActorToken(actorToken, &url.URL{}, ""), nil
}
