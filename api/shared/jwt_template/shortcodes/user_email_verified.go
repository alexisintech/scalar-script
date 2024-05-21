package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/utils/database"
)

type UserEmailVerified struct {
	exec    database.Executor
	user    *model.User
	profile *user_profile.Service
}

func NewUserEmailVerified(clock clockwork.Clock, exec database.Executor, user *model.User) *UserEmailVerified {
	return &UserEmailVerified{
		exec:    exec,
		user:    user,
		profile: user_profile.NewService(clock),
	}
}

func (s *UserEmailVerified) Identifier() string {
	return "user.email_verified"
}

func (s *UserEmailVerified) Substitute(ctx context.Context) (any, error) {
	return s.profile.HasVerifiedEmail(ctx, s.exec, s.user.ID)
}
