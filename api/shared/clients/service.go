package clients

import (
	"context"
	"errors"
	"fmt"
	"time"

	"clerk/api/apierror"
	"clerk/api/shared/client_data"
	"clerk/api/shared/sessions"
	"clerk/api/shared/sign_in"
	"clerk/api/shared/sign_up"
	"clerk/api/shared/token"
	"clerk/model"
	"clerk/pkg/auth"
	"clerk/pkg/cenv"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/request_info"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/jonboulle/clockwork"
)

type Service struct {
	clock clockwork.Clock
	db    database.Database

	// services
	sessionService    *sessions.Service
	signInService     *sign_in.Service
	signUpService     *sign_up.Service
	tokenService      *token.Service
	clientDataService *client_data.Service

	// repositories
	identificationRepo *repository.Identification
	signInRepo         *repository.SignIn
	signUpRepo         *repository.SignUp
	userRepo           *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:              deps.Clock(),
		db:                 deps.DB(),
		sessionService:     sessions.NewService(deps),
		signInService:      sign_in.NewService(deps),
		signUpService:      sign_up.NewService(deps),
		tokenService:       token.NewService(),
		clientDataService:  client_data.NewService(deps),
		identificationRepo: repository.NewIdentification(),
		signInRepo:         repository.NewSignIn(),
		signUpRepo:         repository.NewSignUp(),
		userRepo:           repository.NewUsers(),
	}
}

// EndAllSessions marks all sessions ended
func (s *Service) EndAllSessions(ctx context.Context, instance *model.Instance, client *model.Client) error {
	activeClientSessions, err := s.clientDataService.FindAllClientSessions(ctx, instance.ID, client.ID, &client_data.SessionFilterParams{
		ActiveOnly: true,
	})
	if err != nil {
		return err
	}

	if len(activeClientSessions) > 0 {
		if err := s.userRepo.UpdateUpdatedAtByID(ctx, s.db, activeClientSessions[0].UserID); err != nil {
			return err
		}
	}

	for _, session := range activeClientSessions {
		err := s.sessionService.End(ctx, instance, session.ToSessionModel())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ConvertToClientWithSessions(
	ctx context.Context,
	client *model.Client,
	env *model.Env,
) (*model.ClientWithSessions, apierror.Error) {
	if client == nil {
		return nil, nil
	}

	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	// Reload the client from the database to get the newest one (the updatedAt column might have been updated)
	// In general we should not pass the client model in this function. The optimal solution is to pass the
	// client id and retrieve it from the database to avoid any outdated records.
	reloadedClient, err := s.clientDataService.FindClient(ctx, env.Instance.ID, client.ID)
	if err != nil {
		return nil, apierror.Unexpected(fmt.Errorf("repo: WithSessions %s: %w", client, err))
	}
	client = reloadedClient.ToClientModel()

	clientWithSessions := model.ClientWithSessions{Client: client}

	var currentSessions []*client_data.Session

	if cenv.IsEnabled(cenv.FlagRemoveExpiredSessions) {
		currentSessions, err = s.clientDataService.FindAllCurrentSessionsByClientsWithoutExpiredSessions(ctx, env.Instance.ID, []string{client.ID})
	} else {
		currentSessions, err = s.clientDataService.FindAllCurrentSessionsByClients(ctx, env.Instance.ID, []string{client.ID})
	}

	if err != nil {
		return nil, apierror.Unexpected(
			fmt.Errorf("convertToClientWithSessions: get client's %s current sessions: %w", client, err),
		)
	}

	currentSessionsWithUsers := make([]*model.SessionWithUser, 0)
	for _, session := range currentSessions {
		sessionWithUser, err := s.sessionService.ConvertToSessionWithUser(ctx, env.Instance, userSettings, session.ToSessionModel(), env.AuthConfig)
		if err != nil {
			if apiErr, isAPIErr := apierror.As(err); isAPIErr {
				return nil, apiErr
			}
			return nil, apierror.Unexpected(
				fmt.Errorf("convertToClientWithSessions: converting %+v to session with user: %w", session, err),
			)
		}
		if sessionWithUser == nil {
			continue
		}

		currentSessionsWithUsers = append(currentSessionsWithUsers, sessionWithUser)
	}
	clientWithSessions.CurrentSessions = currentSessionsWithUsers

	requestInfo := request_info.FromContext(ctx)

	var lastTouchedAt *time.Time
	var lastTouched *model.SessionWithUser

	iss, err := s.tokenService.GetIssuer(ctx, s.db, env.Domain, env.Instance)
	if err != nil {
		return nil, apierror.Unexpected(err)
	}

	for i := range clientWithSessions.CurrentSessions {
		sessionWithUser := clientWithSessions.CurrentSessions[i]
		if sessionWithUser.Session.GetStatus(s.clock) != constants.SESSActive {
			continue
		}

		if lastTouchedAt == nil || sessionWithUser.TouchedAt.After(*lastTouchedAt) {
			lastTouchedAt = &sessionWithUser.TouchedAt
			lastTouched = sessionWithUser
		}

		clientWithSessions.CurrentSessions[i].Token, err = token.GenerateSessionToken(
			ctx,
			s.clock,
			s.db,
			env,
			sessionWithUser.Session,
			requestInfo.Origin,
			iss,
		)
		if err != nil && !errors.Is(err, auth.ErrInactiveSession) && !errors.Is(err, token.ErrUserNotFound) {
			return nil, apierror.Unexpected(fmt.Errorf("convertToClientWithSessions: generate session token: %w", err))
		}
	}

	if lastTouched != nil {
		clientWithSessions.LastActiveSession = lastTouched.Session
	}

	// populate SignIn
	if client.SignInID.Valid {
		signIn, err := s.signInRepo.FindByID(ctx, s.db, client.SignInID.String)
		if err != nil {
			return nil, apierror.Unexpected(
				fmt.Errorf("convertToClientWithSessions: fetch sign-in for client %s: %w", client, err),
			)
		}
		signInSerializable, err := s.signInService.ConvertToSerializable(ctx, s.db, signIn, userSettings, "")
		if err != nil {
			return nil, apierror.Unexpected(
				fmt.Errorf("convertToClientWithSessions: fetch sign-in serializable for %+v (%+v): %w", signIn, env, err),
			)
		}
		clientWithSessions.SignIn = signInSerializable
	}

	// populate SignUp
	if client.SignUpID.Valid {
		signUp, err := s.signUpRepo.FindByID(ctx, s.db, client.SignUpID.String)
		if err != nil {
			return nil, apierror.Unexpected(
				fmt.Errorf("convertToClientWithSessions: fetch sign_up for client %s: %w", client, err),
			)
		}
		signUpSerializable, err := s.signUpService.ConvertToSerializable(ctx, s.db, signUp, userSettings, "")
		if err != nil {
			return nil, apierror.Unexpected(
				fmt.Errorf("convertToClientWithSessions: fetch sign-up serializable for %+v (%+v): %w", signUp, env, err),
			)
		}
		clientWithSessions.SignUp = signUpSerializable
	}

	return &clientWithSessions, nil
}
