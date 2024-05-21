package shortcodes

import (
	"context"

	"clerk/model"
)

type UserHasImage struct {
	user *model.User
}

func NewUserHasImage(u *model.User) *UserHasImage {
	return &UserHasImage{
		user: u,
	}
}

func (s *UserHasImage) Identifier() string {
	return "user.has_image"
}

func (s *UserHasImage) Substitute(_ context.Context) (any, error) {
	return s.user.ProfileImagePublicURL.Valid, nil
}
