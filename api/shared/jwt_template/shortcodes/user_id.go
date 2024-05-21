package shortcodes

import (
	"context"

	"clerk/model"
)

type UserID struct {
	user *model.User
}

func NewUserID(u *model.User) *UserID {
	return &UserID{
		user: u,
	}
}

func (s *UserID) Identifier() string {
	return "user.id"
}

func (s *UserID) Substitute(_ context.Context) (any, error) {
	return s.user.ID, nil
}
