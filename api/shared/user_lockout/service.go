package users

import (
	"context"
	"fmt"

	"clerk/api/apierror"
	"clerk/api/serialize"
	"clerk/api/shared/events"
	"clerk/api/shared/serializable"
	"clerk/model"
	usersettings "clerk/pkg/usersettings/clerk"
	"clerk/repository"
	"clerk/utils/clerk"
	"clerk/utils/database"
	"clerk/utils/log"

	"github.com/jonboulle/clockwork"
	"github.com/volatiletech/null/v8"
)

type Service struct {
	clock clockwork.Clock

	// services
	eventService        *events.Service
	serializableService *serializable.Service

	// repositories
	identificationRepo *repository.Identification
	userRepo           *repository.Users
}

func NewService(deps clerk.Deps) *Service {
	return &Service{
		clock:               deps.Clock(),
		eventService:        events.NewService(deps),
		serializableService: serializable.NewService(deps.Clock()),
		identificationRepo:  repository.NewIdentification(),
		userRepo:            repository.NewUsers(),
	}
}

// Lock marks the provided user as locked by setting their locked_at timestamp to now.
func (s *Service) Lock(ctx context.Context, exec database.Executor, env *model.Env, user *model.User) (*model.UserSerializable, error) {
	user.LockedAt = null.TimeFrom(s.clock.Now().UTC())

	err := s.userRepo.UpdateLockedAt(ctx, exec, user)
	if err != nil {
		return nil, fmt.Errorf("user_lockout/lock: lock user %s: %w", user, err)
	}

	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	userSerializable, err := s.sendUserUpdatedEvent(ctx, exec, env.Instance, userSettings, user)
	if err != nil {
		return nil, fmt.Errorf("user_lockout/lock: send user updated event for (%s, %s): %w", user, env.Instance.ID, err)
	}

	log.Debug(ctx, "Locking user", user.ID)

	return userSerializable, nil
}

// Unlock clears a user's locked_at timestamp and resets their failed_attempts to 0.
func (s *Service) Unlock(ctx context.Context, exec database.Executor, env *model.Env, user *model.User) (*model.UserSerializable, error) {
	err := s.userRepo.Unlock(ctx, exec, user)
	if err != nil {
		return nil, fmt.Errorf("user_lockout/reset: unlock user %s: %w", user, err)
	}

	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	userSerializable, err := s.sendUserUpdatedEvent(ctx, exec, env.Instance, userSettings, user)
	if err != nil {
		return nil, fmt.Errorf("user_lockout/reset: send user updated event for (%s, %s): %w", user, env.Instance.ID, err)
	}

	log.Debug(ctx, "Resetting user lockout fields for user", user.ID)

	return userSerializable, nil
}

// IncrementFailedVerificationAttempts increments the user's failed_verification_attempts
// If max_failed_verification_attempts is reached, the user will be locked
// NOP if feature is not enabled
func (s *Service) IncrementFailedVerificationAttempts(ctx context.Context, exec database.Executor, env *model.Env, user *model.User) error {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.UserLockoutEnabled() {
		return nil
	}

	err := s.userRepo.IncrementFailedVerificationAttempts(ctx, exec, user)
	if err != nil {
		return fmt.Errorf("user_lockout/incrementFailedVerificationAttempts: increment user.failed_verification_attempts %s: %w", user, err)
	}

	if user.FailedVerificationAttempts >= userSettings.AttackProtection.UserLockout.GetMaxAttempts() {
		_, err := s.Lock(ctx, exec, env, user)
		if err != nil {
			return fmt.Errorf("user_lockout/incrementFailedVerificationAttempts: lock user %s: %w", user, err)
		}
	}

	return nil
}

// ResetFailedVerificationAttempts sets the user's failed_verification_attempts to 0
// NOP if feature is not enabled
func (s *Service) ResetFailedVerificationAttempts(ctx context.Context, exec database.Executor, env *model.Env, user *model.User) error {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.UserLockoutEnabled() {
		return nil
	}

	user.FailedVerificationAttempts = 0

	err := s.userRepo.UpdateFailedVerificationAttempts(ctx, exec, user)
	if err != nil {
		return fmt.Errorf("user_lockout/incrementFailedVerificationAttempts: increment user.failed_verification_attempts %s: %w", user, err)
	}

	return nil
}

func (s *Service) EnsureUserNotLocked(ctx context.Context, exec database.Executor, env *model.Env, user *model.User) apierror.Error {
	userSettings := usersettings.NewUserSettings(env.AuthConfig.UserSettings)

	if !userSettings.UserLockoutEnabled() {
		return nil
	}

	userLockoutStatus := user.LockoutStatus(s.clock, userSettings.AttackProtection.UserLockout)

	if userLockoutStatus.Locked {
		return apierror.UserLocked((*apierror.UserLockoutStatus)(userLockoutStatus), env.Instance.Communication.SupportEmail.Ptr())
	}

	// Clear an expired lock, if applicable
	if user.LockedAt.Valid {
		_, err := s.Unlock(ctx, exec, env, user)
		if err != nil {
			return apierror.Unexpected(err)
		}
	}

	return nil
}

func (s *Service) sendUserUpdatedEvent(
	ctx context.Context,
	exec database.Executor,
	instance *model.Instance,
	userSettings *usersettings.UserSettings,
	user *model.User,
) (*model.UserSerializable, error) {
	userSerializable, err := s.serializableService.ConvertUser(ctx, exec, userSettings, user)
	if err != nil {
		return nil, fmt.Errorf("user_lockout/sendUserUpdatedEvent: serializing user %+v: %w", user, err)
	}

	if err = s.eventService.UserUpdated(ctx, exec, instance, serialize.UserToServerAPI(ctx, userSerializable)); err != nil {
		return nil, fmt.Errorf("user_lockout/sendUserUpdatedEvent: send user updated event for user %s: %w", user.ID, err)
	}

	return userSerializable, nil
}
