package sessions

import (
	"context"
	"errors"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/cookies"
	"clerk/api/shared/events"
	"clerk/api/shared/jwt"
	"clerk/api/shared/sessions"
	"clerk/pkg/auth"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	sentryclerk "clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"

	"github.com/jonboulle/clockwork"
)

// Service is the service layer for all operations related to session in the server API.
// It contains all the business logic.
type Service struct {
	clock     clockwork.Clock
	db        database.Database
	validator *validator.Validate

	// services
	cookieService  *cookies.Service
	eventService   *events.Service
	jwtService     *jwt.Service
	sessionService *sessions.Service

	// repositories
	sessionsRepo *repository.Sessions
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:          deps.Clock(),
		db:             deps.DB(),
		validator:      validator.New(),
		cookieService:  cookies.NewService(deps),
		eventService:   events.NewService(deps),
		jwtService:     jwt.NewService(deps.Clock()),
		sessionService: sessions.NewService(deps),
		sessionsRepo:   repository.NewSessions(deps.Clock()),
	}
}

// Read returns the session that is loaded into the context.
// Make sure there is a call to SetSession before calling this.
func (s *Service) Read(ctx context.Context, sessionID string) (*serialize.SessionServerResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	session, err := s.sessionsRepo.QueryByIDAndInstanceID(ctx, s.db, sessionID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if session == nil {
		return nil, apierror.SessionNotFound(sessionID)
	}

	return serialize.SessionToServerAPI(s.clock, session), nil
}

// Revoke marks the given session as revoked.
func (s *Service) Revoke(ctx context.Context, sessionID string) (*serialize.SessionServerResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	sessionToRevoke, goerr := s.sessionsRepo.QueryByIDAndInstanceID(ctx, s.db, sessionID, env.Instance.ID)
	if goerr != nil {
		return nil, apierror.Unexpected(goerr)
	} else if sessionToRevoke == nil {
		return nil, apierror.SessionNotFound(sessionID)
	}

	err := s.sessionService.Revoke(ctx, env.Instance, sessionToRevoke)
	if err != nil {
		return nil, err
	}

	return serialize.SessionToServerAPI(s.clock, sessionToRevoke), nil
}

type VerifyParams struct {
	Token     string `json:"token" form:"token" validate:"required"`
	SessionID string `json:"-" form:"-"`
}

func (p VerifyParams) validate(validator *validator.Validate) apierror.Error {
	if err := validator.Struct(p); err != nil {
		return apierror.FormValidationFailed(err)
	}
	return nil
}

// VerifySession verifies the given token and returns the requested session.
func (s *Service) Verify(ctx context.Context, params VerifyParams) (*serialize.SessionServerResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	if err := params.validate(s.validator); err != nil {
		return nil, err
	}

	session, err := s.sessionsRepo.QueryByIDAndInstanceID(ctx, s.db, params.SessionID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if session == nil {
		return nil, apierror.SessionNotFound(params.SessionID)
	}

	claims, err := auth.VerifySessionToken(params.Token, env.Instance, s.clock)
	if err != nil {
		return nil, apierror.InvalidSessionToken()
	}

	claimsSID, ok := claims["sid"].(string)
	if !ok {
		return nil, apierror.InvalidSessionToken()
	}

	if claimsSID != session.ID {
		return nil, apierror.SessionNotFound(claimsSID)
	}

	if session.GetStatus(s.clock) != constants.SESSActive {
		return nil, apierror.SessionNotFound(params.SessionID)
	}

	return serialize.SessionToServerAPI(s.clock, session), nil
}

func (s *Service) CreateTokenFromTemplate(ctx context.Context, sessionID, templateName string) (*serialize.TokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)

	session, err := s.sessionsRepo.QueryByIDAndInstanceID(ctx, s.db, sessionID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if session == nil {
		return nil, apierror.SessionNotFound(sessionID)
	}

	if !session.IsActive(s.clock) {
		return nil, apierror.SessionNotFound(sessionID)
	}

	token, err := s.jwtService.CreateFromTemplate(ctx, s.db, jwt.CreateFromTemplateParams{
		Env:          env,
		UserID:       session.UserID,
		ActiveOrgID:  session.ActiveOrganizationID.Ptr(),
		TemplateName: templateName,
		Actor:        session.Actor.JSON,
	})
	if err != nil {
		if errors.Is(err, jwt.ErrUserNotFound) {
			return nil, apierror.UserNotFound(session.UserID)
		} else if errors.Is(err, jwt.ErrJWTTemplateNotFound) {
			return nil, apierror.JWTTemplateNotFound("name", templateName)
		}
		return nil, apierror.Unexpected(err)
	}

	txErr := s.db.PerformTx(ctx, func(tx database.Tx) (bool, error) {
		err = s.eventService.TokenCreated(ctx, tx, env.Instance, token, session.UserID, nil)
		if err != nil {
			return true, err
		}

		return false, nil
	})
	if txErr != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and
		// continue
		sentryclerk.CaptureException(ctx, txErr)
	}

	return serialize.Token(token), nil
}
