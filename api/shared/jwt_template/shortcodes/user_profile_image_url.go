package shortcodes

import (
	"context"

	"github.com/jonboulle/clockwork"

	"clerk/api/shared/user_profile"
	"clerk/model"
)

type UserProfileImageURL struct {
	user    *model.User
	profile *user_profile.Service
}

func NewUserProfileImageURL(clock clockwork.Clock, user *model.User) *UserProfileImageURL {
	return &UserProfileImageURL{
		user:    user,
		profile: user_profile.NewService(clock),
	}
}

func (s *UserProfileImageURL) Identifier() string {
	return "user.profile_image_url"
}

func (s *UserProfileImageURL) Substitute(_ context.Context) (any, error) {
	url, _ := s.profile.GetProfileImageURL(s.user)
	return url, nil
}
