package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
	"clerk/utils/database"
)

type UserPrimaryWeb3Wallet struct {
	exec    database.Executor
	user    *model.User
	profile *user_profile.Service
}

func NewUserPrimaryWeb3Wallet(clock clockwork.Clock, exec database.Executor, user *model.User) *UserPrimaryWeb3Wallet {
	return &UserPrimaryWeb3Wallet{
		exec:    exec,
		user:    user,
		profile: user_profile.NewService(clock),
	}
}

func (s *UserPrimaryWeb3Wallet) Identifier() string {
	return "user.primary_web3_wallet"
}

func (s *UserPrimaryWeb3Wallet) Substitute(ctx context.Context) (any, error) {
	return s.profile.GetPrimaryWeb3Wallet(ctx, s.exec, s.user)
}
