package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/utils/database"
)

type UserUsername struct {
	exec    database.Executor
	user    *model.User
	profile *user_profile.Service
}

func NewUserUsername(exec database.Executor, clock clockwork.Clock, user *model.User) *UserUsername {
	return &UserUsername{
		exec:    exec,
		user:    user,
		profile: user_profile.NewService(clock),
	}
}

func (s *UserUsername) Identifier() string {
	return "user.username"
}

func (s *UserUsername) Substitute(ctx context.Context) (any, error) {
	return s.profile.GetUsername(ctx, s.exec, s.user)
}
