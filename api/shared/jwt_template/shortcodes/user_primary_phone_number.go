package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/utils/database"
)

type UserPrimaryPhoneNumber struct {
	exec    database.Executor
	user    *model.User
	profile *user_profile.Service
}

func NewUserPrimaryPhoneNumber(clock clockwork.Clock, exec database.Executor, user *model.User) *UserPrimaryPhoneNumber {
	return &UserPrimaryPhoneNumber{
		exec:    exec,
		user:    user,
		profile: user_profile.NewService(clock),
	}
}

func (s *UserPrimaryPhoneNumber) Identifier() string {
	return "user.primary_phone_number"
}

func (s *UserPrimaryPhoneNumber) Substitute(ctx context.Context) (any, error) {
	return s.profile.GetPrimaryPhoneNumber(ctx, s.exec, s.user)
}
