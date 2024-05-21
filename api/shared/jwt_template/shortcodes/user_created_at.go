package shortcodes

import (
	"context"

	"clerk/model"
)

type UserCreatedAt struct {
	user *model.User
}

func NewUserCreatedAt(u *model.User) *UserCreatedAt {
	return &UserCreatedAt{
		user: u,
	}
}

func (s *UserCreatedAt) Identifier() string {
	return "user.created_at"
}

func (s *UserCreatedAt) Substitute(_ context.Context) (any, error) {
	return s.user.CreatedAt.Unix(), nil
}
