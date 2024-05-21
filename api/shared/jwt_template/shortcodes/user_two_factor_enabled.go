package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
	settings "clerk/pkg/usersettings/clerk"
	"clerk/utils/database"
)

type UserTwoFactorEnabled struct {
	exec     database.Executor
	user     *model.User
	settings *settings.UserSettings
	profile  *user_profile.Service
}

func NewUserTwoFactorEnabled(exec database.Executor, clock clockwork.Clock,
	user *model.User, settings *settings.UserSettings) *UserTwoFactorEnabled {
	return &UserTwoFactorEnabled{
		exec:     exec,
		settings: settings,
		user:     user,
		profile:  user_profile.NewService(clock),
	}
}

func (s *UserTwoFactorEnabled) Identifier() string {
	return "user.two_factor_enabled"
}

func (s *UserTwoFactorEnabled) Substitute(ctx context.Context) (any, error) {
	return s.profile.HasTwoFactorEnabled(ctx, s.exec, s.settings, s.user.ID)
}
