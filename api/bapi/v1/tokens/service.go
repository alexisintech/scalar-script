package tokens

import (
	"context"
	"errors"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/jwt"
	"clerk/pkg/ctx/environment"
	sentryclerk "clerk/pkg/sentry"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"
)

type Service struct {
	db        database.Database
	validator *validator.Validate

	eventService *events.Service
	jwtService   *jwt.Service
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:           deps.DB(),
		validator:    validator.New(),
		eventService: events.NewService(deps),
		jwtService:   jwt.NewService(deps.Clock()),
	}
}

type CreateFromTemplateParams struct {
	TemplateName string `json:"name" form:"name" validate:"required"`
	UserID       string `json:"user_id" form:"user_id" validate:"required"`
}

func (p CreateFromTemplateParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}

	return nil
}

// CreateFromTemplate creates a new JWT according to the provided template.
func (s *Service) CreateFromTemplate(ctx context.Context, params CreateFromTemplateParams) (*serialize.TokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if apiErr := params.validate(s.validator); apiErr != nil {
		return nil, apiErr
	}

	token, err := s.jwtService.CreateFromTemplate(ctx, s.db, jwt.CreateFromTemplateParams{
		Env:          env,
		UserID:       params.UserID,
		TemplateName: params.TemplateName,
	})
	if err != nil {
		if errors.Is(err, jwt.ErrUserNotFound) {
			return nil, apierror.UserNotFound(params.UserID)
		} else if errors.Is(err, jwt.ErrJWTTemplateNotFound) {
			return nil, apierror.JWTTemplateNotFound("name", params.TemplateName)
		}
		return nil, apierror.Unexpected(err)
	}

	err = s.eventService.TokenCreated(ctx, s.db, env.Instance, token, params.UserID, nil)
	if err != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and
		// continue
		sentryclerk.CaptureException(ctx, err)
	}

	return serialize.Token(token), nil
}
