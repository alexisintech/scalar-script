package clients

import (
	"context"
	"fmt"
	"time"

	"clerk/api/shared/client_data"
	"clerk/api/shared/clients"
	"clerk/api/shared/cookies"
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/ctx/environment"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/go-playground/validator/v10"

	"github.com/jonboulle/clockwork"
)

// Service contains the business logic of all operations related to client in the server API.
type Service struct {
	db        database.Database
	clock     clockwork.Clock
	validator *validator.Validate

	// services
	cookieService     *cookies.Service
	clientService     *clients.Service
	clientDataService *client_data.Service

	// repositories
	clientRepo *repository.Clients
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		db:                deps.DB(),
		clock:             deps.Clock(),
		validator:         validator.New(),
		cookieService:     cookies.NewService(deps),
		clientService:     clients.NewService(deps),
		clientDataService: client_data.NewService(deps),
		clientRepo:        repository.NewClients(),
	}
}

func (s *Service) ConvertClientForBAPI(ctx context.Context, client *model.Client) (*model.ClientWithSessionsForBAPI, error) {
	clientsWithSessions, err := s.ConvertClientsForBAPI(ctx, []*model.Client{client})
	if err != nil {
		return nil, err
	}

	if len(clientsWithSessions) != 1 {
		return nil, fmt.Errorf("convert client expected to get 1 client serializable, got %d instead: %v",
			len(clientsWithSessions), clientsWithSessions)
	}

	return clientsWithSessions[0], nil
}

func (s *Service) ConvertClientsForBAPI(ctx context.Context, clients []*model.Client) ([]*model.ClientWithSessionsForBAPI, error) {
	clientIDs := make([]string, len(clients))
	for i, client := range clients {
		clientIDs[i] = client.ID
	}

	// Eagerly load sessions
	env := environment.FromContext(ctx)
	currentSessions, err := s.clientDataService.FindAllCurrentSessionsByClients(ctx, env.Instance.ID, clientIDs)
	if err != nil {
		return nil, fmt.Errorf("ConvertClients: get current sessions for clients %+v: %w",
			clientIDs, err)
	}

	currentSessionsByClientID := map[string][]*model.Session{}
	for _, session := range currentSessions {
		currentSessionsByClientID[session.ClientID] = append(currentSessionsByClientID[session.ClientID], session.ToSessionModel())
	}

	// Determine last active session by client

	lastActiveSessionByClientID := map[string]*model.Session{}

	for clientID, sessions := range currentSessionsByClientID {
		var lastTouchedAt *time.Time
		var lastTouched *model.Session

		for _, session := range sessions {
			if session.GetStatus(s.clock) != constants.SESSActive {
				continue
			}

			if lastTouchedAt == nil || session.TouchedAt.After(*lastTouchedAt) {
				lastTouchedAt = &session.TouchedAt
				lastTouched = session
			}
		}

		if lastTouched != nil {
			lastActiveSessionByClientID[clientID] = lastTouched
		}
	}

	// Construct result array

	result := make([]*model.ClientWithSessionsForBAPI, len(clients))

	for i, client := range clients {
		clientWithSessions := &model.ClientWithSessionsForBAPI{Client: client}

		// populate CurrentSessions
		clientWithSessions.CurrentSessions = currentSessionsByClientID[client.ID]

		// populate LastActiveSessionID
		lastActiveSession, ok := lastActiveSessionByClientID[client.ID]
		if ok {
			clientWithSessions.LastActiveSessionID = &lastActiveSession.ID
		}

		result[i] = clientWithSessions
	}

	return result, nil
}
