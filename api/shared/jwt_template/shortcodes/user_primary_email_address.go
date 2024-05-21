package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/utils/database"
)

type UserPrimaryEmailAddress struct {
	exec    database.Executor
	user    *model.User
	profile *user_profile.Service
}

func NewUserPrimaryEmailAddress(clock clockwork.Clock, exec database.Executor, user *model.User) *UserPrimaryEmailAddress {
	return &UserPrimaryEmailAddress{
		exec:    exec,
		user:    user,
		profile: user_profile.NewService(clock),
	}
}

func (s *UserPrimaryEmailAddress) Identifier() string {
	return "user.primary_email_address"
}

func (s *UserPrimaryEmailAddress) Substitute(ctx context.Context) (any, error) {
	return s.profile.GetPrimaryEmailAddress(ctx, s.exec, s.user)
}
