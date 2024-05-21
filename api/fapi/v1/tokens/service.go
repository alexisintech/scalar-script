package tokens

import (
	"context"
	"errors"
	"time"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/events"
	"clerk/api/shared/jwt"
	"clerk/api/shared/organizations"
	"clerk/api/shared/token"
	"clerk/model"
	"clerk/pkg/auth"
	"clerk/pkg/ctx/environment"
	"clerk/pkg/ctx/maintenance"
	"clerk/pkg/ctx/request_info"
	"clerk/pkg/ctx/requesting_session"
	"clerk/pkg/externalapis/segment"
	"clerk/pkg/jwt_services"
	"clerk/pkg/segment/fapi"
	sentryclerk "clerk/pkg/sentry"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/param"

	"github.com/jonboulle/clockwork"
	"github.com/vgarvardt/gue/v2"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock     clockwork.Clock
	db        database.Database
	gueClient *gue.Client

	// services
	eventService      *events.Service
	jwtService        *jwt.Service
	orgService        *organizations.Service
	tokenService      *token.Service
	clientDataService *client_data.Service

	// repositories
	jwtServicesRepo *repository.JWTServices
	usersRepo       *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:             deps.Clock(),
		db:                deps.DB(),
		gueClient:         deps.GueClient(),
		eventService:      events.NewService(deps),
		jwtService:        jwt.NewService(deps.Clock()),
		orgService:        organizations.NewService(deps),
		tokenService:      token.NewService(),
		clientDataService: client_data.NewService(deps),
		jwtServicesRepo:   repository.NewJWTServices(),
		usersRepo:         repository.NewUsers(),
	}
}

// CreateForJWTService creates a new token for the given service
func (s *Service) CreateForJWTService(ctx context.Context, user *model.User, serviceType model.JWTServiceType) (*serialize.TokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	session := requesting_session.FromContext(ctx)
	jwtSvc, apiErr := s.getJWTService(ctx, env.AuthConfig, serviceType)
	if apiErr != nil {
		return nil, apiErr
	}
	vendor, err := jwt_services.GetVendor(serviceType)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	issuer := env.Domain.FapiURL()
	token, err := vendor.GenerateToken(ctx, s.db, env, jwtSvc, user, issuer)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	actorID, err := session.ActorID()
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	err = s.eventService.TokenCreated(ctx, s.db, env.Instance, token, user.ID, actorID)
	if err != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and
		// continue
		sentryclerk.CaptureException(ctx, err)
	}
	return serialize.Token(token), nil
}

// CreateFromTemplate creates a new JWT according to the provided template.
func (s *Service) CreateFromTemplate(ctx context.Context, templateName, sessionID, origin string) (*serialize.TokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	session, sessionErr := s.loadSessionFromCtx(ctx, sessionID)
	if sessionErr != nil {
		return nil, sessionErr
	}

	token, err := s.jwtService.CreateFromTemplate(ctx, s.db, jwt.CreateFromTemplateParams{
		Env:          env,
		UserID:       session.UserID,
		ActiveOrgID:  session.ActiveOrganizationID.Ptr(),
		TemplateName: templateName,
		Origin:       origin,
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

	actorID, err := session.ActorID()
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	err = s.eventService.TokenCreated(ctx, s.db, env.Instance, token, session.UserID, actorID)
	if err != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and
		// continue
		sentryclerk.CaptureException(ctx, err)
	}

	return serialize.Token(token), nil
}

// CreateFromFirebase creates a new JWT for a Firebase jwt_service
func (s *Service) CreateFromFirebase(ctx context.Context, sessionID string) (*serialize.TokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	session, sessionErr := s.loadSessionFromCtx(ctx, sessionID)
	if sessionErr != nil {
		return nil, sessionErr
	}

	user, err := s.usersRepo.QueryByIDAndInstance(ctx, s.db, session.UserID, env.Instance.ID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if user == nil {
		return nil, apierror.UnauthorizedActionForSession(sessionID)
	}

	jwtSvc, apiErr := s.getJWTService(ctx, env.AuthConfig, model.JWTServiceTypeFirebase)
	if apiErr != nil {
		return nil, apiErr
	}

	vendor, err := jwt_services.GetVendor(model.JWTServiceTypeFirebase)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	issuer := env.Domain.FapiURL()
	token, err := vendor.GenerateToken(ctx, s.db, env, jwtSvc, user, issuer)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	actorID, err := session.ActorID()
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	err = s.eventService.TokenCreated(ctx, s.db, env.Instance, token, user.ID, actorID)
	if err != nil {
		// Not fatal if we fail saving/delivering an event, so we only log and
		// continue
		sentryclerk.CaptureException(ctx, err)
	}

	return serialize.Token(token), nil
}

func (s *Service) CreateSessionToken(ctx context.Context, sessionID string) (*serialize.TokenResponse, apierror.Error) {
	env := environment.FromContext(ctx)
	requestInfo := request_info.FromContext(ctx)
	session, sessionErr := s.loadSessionFromCtx(ctx, sessionID)
	if sessionErr != nil {
		return nil, sessionErr
	}

	iss, err := s.tokenService.GetIssuer(ctx, s.db, env.Domain, env.Instance)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	newToken, err := token.GenerateSessionToken(
		ctx,
		s.clock,
		s.db,
		env,
		session,
		requestInfo.Origin,
		iss,
	)
	if errors.Is(err, auth.ErrInactiveSession) || errors.Is(err, token.ErrUserNotFound) {
		return nil, apierror.InvalidAuthentication()
	} else if err != nil {
		return nil, apierror.Unexpected(err)
	}

	var eventSent bool
	yesterday := s.clock.Now().UTC().Add(-24 * time.Hour)
	if session.TokenCreatedEventSentAt.Time.Before(yesterday) {
		err := s.eventService.SessionTokenCreated(ctx, s.db, env.Instance, session)
		if err != nil {
			// Not fatal if we fail saving/delivering an event, so we only log and
			// continue
			sentryclerk.CaptureException(ctx, err)
		} else if !maintenance.FromContext(ctx) {
			eventSent = true
		}

		// Also propagate to Segment
		// Exclude impersonation
		if !session.HasActor() {
			fapi.EnqueueSegmentEvent(ctx, s.gueClient, fapi.SegmentParams{EventName: segment.APIFrontendSessionTokenCreated})
		}
	}

	session.TokenIssuedAt = null.TimeFrom(s.clock.Now().UTC())
	if eventSent {
		session.TokenCreatedEventSentAt = session.TokenIssuedAt
		clientDataSession := &client_data.Session{}
		clientDataSession.CopyFromSessionModel(session)
		if err = s.clientDataService.UpdateSessionTokenIssuedAtAndTokenCreatedEventSentAt(ctx, clientDataSession); err != nil {
			return nil, apierror.Unexpected(err)
		}
	} else {
		clientDataSession := &client_data.Session{}
		clientDataSession.CopyFromSessionModel(session)
		if err = s.clientDataService.UpdateSessionTokenIssuedAt(ctx, clientDataSession); err != nil {
			return nil, apierror.Unexpected(err)
		}
	}

	if session.ActiveOrganizationID.Valid {
		err = s.orgService.EmitActiveOrganizationEventIfNeeded(ctx, s.db, session.ActiveOrganizationID.String, env.Instance)
		if err != nil {
			// Not fatal if we fail saving/delivering an event, so we only log and
			// continue
			sentryclerk.CaptureException(ctx, err)
		}
	}

	return serialize.Token(newToken), nil
}

func (s *Service) getJWTService(ctx context.Context, authConfig *model.AuthConfig, jwtSvcType model.JWTServiceType) (*model.JWTService, apierror.Error) {
	// Check if this is JWT service type we support
	if !jwt_services.VendorExists(jwtSvcType) {
		return nil, apierror.FormInvalidParameterValue(param.Service.Name, string(jwtSvcType))
	}

	jwtSvc, err := s.jwtServicesRepo.QueryByAuthConfigIDAndType(ctx, s.db, authConfig.ID, jwtSvcType)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}
	if jwtSvc == nil {
		return nil, apierror.FormDisabledParameterValue(param.Service.Name, string(jwtSvcType))
	}

	return jwtSvc, nil
}

// LoadSessionFromCtx fetches session from current context
func (s *Service) loadSessionFromCtx(ctx context.Context, sessionID string) (*model.Session, apierror.Error) {
	session := requesting_session.FromContext(ctx)
	if session == nil {
		return nil, apierror.SessionNotFound(sessionID)
	}
	return session, nil
}
