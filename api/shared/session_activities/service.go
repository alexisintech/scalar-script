package session_activities

import (
	"clerk/model"
	"clerk/pkg/clerkerrors"
	"clerk/repository"
	"clerk/utils/database"
	"context"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	sessionActivitiesRepo *repository.SessionActivities
}

func NewService() *Service {
	return &Service{
		sessionActivitiesRepo: repository.NewSessionActivities(),
	}
}

// CreateSessionActivity creates a SessionActivity record, ensuring that the InstanceID is set.
// This is needed as the Middleware that parses out the headers & creates the Session Activity struct
// does not have access to the InstanceID at the time that the middleware executes.
func (s *Service) CreateSessionActivity(
	ctx context.Context,
	db database.Executor,
	instanceID string,
	sessionActivity *model.SessionActivity,
) error {
	sessionActivity.InstanceID = null.StringFrom(instanceID)
	if err := s.sessionActivitiesRepo.Insert(ctx, db, sessionActivity); err != nil {
		return clerkerrors.WithStacktrace("session_activities.Service: CreateSessionActivity %s: %w", sessionActivity, err)
	}
	return nil
}
