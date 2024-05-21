package password

import (
	"context"
	"fmt"
	"strings"

	"clerk/api/serialize"
	"clerk/api/shared/client_data"
	"clerk/api/shared/comms"
	"clerk/api/shared/events"
	"clerk/api/shared/serializable"
	"clerk/api/shared/sessions"
	"clerk/api/shared/user_profile"
	"clerk/model"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"

	"github.com/volatiletech/null/v8"
)

type Service struct {
	commService         *comms.Service
	eventService        *events.Service
	sessionService      *sessions.Service
	serializableService *serializable.Service
	userProfileService  *user_profile.Service
	clientDataService   *client_data.Service
	userRepo            *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		commService:         comms.NewService(deps),
		eventService:        events.NewService(deps),
		sessionService:      sessions.NewService(deps),
		serializableService: serializable.NewService(deps.Clock()),
		userProfileService:  user_profile.NewService(deps.Clock()),
		clientDataService:   client_data.NewService(deps),
		userRepo:            repository.NewUsers(),
	}
}

type ChangeUserPasswordParams struct {
	Env                    *model.Env
	PasswordDigest         string
	PasswordHasher         string
	RequestingSessionID    *string
	SignOutOfOtherSessions bool
	User                   *model.User
}

func (s *Service) ChangeUserPassword(ctx context.Context, tx database.Tx, params ChangeUserPasswordParams) error {
	params.User.PasswordDigest = null.StringFrom(params.PasswordDigest)
	params.User.PasswordHasher = null.StringFrom(params.PasswordHasher)

	err := s.userRepo.UpdatePasswordDigestAndHasher(ctx, tx, params.User)
	if err != nil {
		return fmt.Errorf("changeUserPassword: updating password digest and hasher for user %s: %w",
			params.User.ID, err)
	}

	if params.SignOutOfOtherSessions && params.RequestingSessionID != nil {
		// Note that the following operation happens outside the database transaction.
		// This is also desirable in order to reduce chance of race conditions due to transaction isolation.
		err := s.signUserOutOfOtherSessions(ctx, params.User.ID, params.Env.Instance, *params.RequestingSessionID)
		if err != nil {
			return fmt.Errorf("changeUserPassword: signing out all user's %s active sessions except for %s: %w",
				params.User.ID, *params.RequestingSessionID, err)
		}
	}

	err = s.sendPasswordChangedNotification(ctx, tx, params.Env, params.User)
	if err != nil {
		return fmt.Errorf("changeUserPassword: sending password changed notification for %s: %w",
			params.User.ID, err)
	}

	userSerializable, err := s.serializableService.ConvertUser(ctx, tx, usersettings.NewUserSettings(params.Env.AuthConfig.UserSettings), params.User)
	if err != nil {
		return err
	}
	if err = s.eventService.UserUpdated(ctx, tx, params.Env.Instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return fmt.Errorf("changeUserPassword: send user updated event for (%s, %s): %w",
			params.User.ID, params.Env.Instance.ID, err)
	}
	return nil
}

// signUserOutOfOtherSessions fetches all active sessions for the user with userID and ends them all
// except the one with currentSessionID.
func (s *Service) signUserOutOfOtherSessions(
	ctx context.Context,
	userID string,
	instance *model.Instance,
	currentSessionID string,
) error {
	activeSessions, err := s.clientDataService.FindAllUserSessions(ctx, instance.ID, userID, client_data.SessionFilterActiveOnly())
	if err != nil {
		return err
	}
	for _, session := range activeSessions {
		if session.ID == currentSessionID {
			continue
		}
		err := s.sessionService.End(ctx, instance, session.ToSessionModel())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) sendPasswordChangedNotification(
	ctx context.Context,
	tx database.Tx,
	env *model.Env,
	user *model.User) error {
	primaryEmailAddress, err := s.userProfileService.GetPrimaryEmailAddress(ctx, tx, user)
	if err != nil {
		return err
	}
	if primaryEmailAddress != nil {
		return s.commService.SendPasswordChangedEmail(ctx, tx, env, comms.EmailPasswordChanged{
			GreetingName: strings.TrimSpace(fmt.Sprintf("%s %s",
				user.FirstName.String, user.LastName.String)),
			PrimaryEmailAddress: *primaryEmailAddress,
		})
	}

	primaryPhoneNumber, err := s.userProfileService.GetPrimaryPhoneNumber(ctx, tx, user)
	if err != nil {
		return err
	}
	if primaryPhoneNumber != nil {
		return s.commService.SendPasswordChangedSMS(ctx, tx, env, *primaryPhoneNumber)
	}
	return nil
}
