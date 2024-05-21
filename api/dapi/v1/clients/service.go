package clients

import (
	"context"
	"errors"

	"clerk/api/apierror"
	"clerk/api/shared/client_data"
	"clerk/model"
	"clerk/pkg/clerkerrors"
	"clerk/utils/clerk"
	"clerk/utils/database"

	sdk "github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/clerk/clerk-sdk-go/v2/jwt"
)

type Service struct {
	db                database.Database
	jwksClient        *jwks.Client
	clientDataService *client_data.Service
}

func NewService(deps clerk.Deps, jwksClient *jwks.Client) *Service {
	return &Service{
		db:                deps.DB(),
		jwksClient:        jwksClient,
		clientDataService: client_data.NewService(deps),
	}
}

// VerifyClientFromJWT calls the server api to verify a client from the session cookie & aborts if a client can't be determined
func (s *Service) VerifyClientFromJWT(ctx context.Context, token string) (*model.Client, apierror.Error) {
	sessionClaims, err := jwt.Verify(ctx, &jwt.VerifyParams{
		Token:      token,
		JWKSClient: s.jwksClient,
	})
	if err != nil {
		return nil, apierror.ClientNotFoundInRequest()
	}

	dashboardInstanceID, apiErr := s.dashboardInstanceID(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	userID := sessionClaims.Subject
	session, err := s.clientDataService.FindUserSession(ctx, dashboardInstanceID, userID, sessionClaims.SessionID)
	if err != nil {
		return nil, apierror.Unexpected(err)
	} else if session == nil {
		return nil, apierror.SessionNotFound(sessionClaims.SessionID)
	}

	return s.GetClient(ctx, session.ClientID)
}

// GetClient retrieves the client based on the provided ID. If not found returns an error
func (s *Service) GetClient(ctx context.Context, clientID string) (*model.Client, apierror.Error) {
	dashboardInstanceID, apiErr := s.dashboardInstanceID(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	client, err := s.clientDataService.FindClient(ctx, dashboardInstanceID, clientID)
	if err != nil {
		if errors.Is(err, client_data.ErrNoRecords) {
			return nil, apierror.ClientNotFound(clientID)
		}
		return nil, apierror.Unexpected(err)
	} else if client == nil {
		return nil, apierror.ClientNotFound(clientID)
	}

	return client.ToClientModel(), nil
}

func (s *Service) DashboardSessionFromClaims(ctx context.Context, claims *sdk.SessionClaims) (*model.Session, apierror.Error) {
	dashboardInstanceID, apiErr := s.dashboardInstanceID(ctx)
	if apiErr != nil {
		return nil, apiErr
	}
	session, err := s.clientDataService.FindUserSession(ctx, dashboardInstanceID, claims.Subject, claims.SessionID)
	if err != nil {
		return nil, apierror.SessionNotFound(claims.SessionID)
	}
	return session.ToSessionModel(), nil
}

func (s *Service) dashboardInstanceID(ctx context.Context) (string, apierror.Error) {
	jwks, err := s.jwksClient.Get(ctx, &jwks.GetParams{})
	if err != nil {
		return "", apierror.Unexpected(err)
	}
	if len(jwks.Keys) == 0 || len(jwks.Keys[0].KeyID) == 0 {
		return "", apierror.Unexpected(clerkerrors.WithStacktrace("dashboard instance ID cannot be retrieved from the JWKS endpoint"))
	}
	return jwks.Keys[0].KeyID, nil
}
