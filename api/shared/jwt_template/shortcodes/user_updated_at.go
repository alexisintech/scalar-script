package shortcodes

import (
	"context"

	"clerk/model"
)

type UserUpdatedAt struct {
	user *model.User
}

func NewUserUpdatedAt(u *model.User) *UserUpdatedAt {
	return &UserUpdatedAt{
		user: u,
	}
}

func (s *UserUpdatedAt) Identifier() string {
	return "user.updated_at"
}

func (s *UserUpdatedAt) Substitute(_ context.Context) (any, error) {
	return s.user.UpdatedAt.Unix(), nil
}
