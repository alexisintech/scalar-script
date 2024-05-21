package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
)

type UserImageURL struct {
	user    *model.User
	profile *user_profile.Service
}

func NewUserImageURL(clock clockwork.Clock, user *model.User) *UserImageURL {
	return &UserImageURL{
		user:    user,
		profile: user_profile.NewService(clock),
	}
}

func (s *UserImageURL) Identifier() string {
	return "user.image_url"
}

func (s *UserImageURL) Substitute(_ context.Context) (any, error) {
	return s.profile.GetImageURL(s.user)
}
