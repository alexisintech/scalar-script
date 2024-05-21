package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/utils/database"
)

type UserPhoneNumberVerified struct {
	exec    database.Executor
	user    *model.User
	profile *user_profile.Service
}

func NewUserPhoneNumberVerified(clock clockwork.Clock, exec database.Executor, user *model.User) *UserPhoneNumberVerified {
	return &UserPhoneNumberVerified{
		exec:    exec,
		user:    user,
		profile: user_profile.NewService(clock),
	}
}

func (s *UserPhoneNumberVerified) Identifier() string {
	return "user.phone_number_verified"
}

func (s *UserPhoneNumberVerified) Substitute(ctx context.Context) (any, error) {
	return s.profile.HasVerifiedPhone(ctx, s.exec, s.user.ID)
}
