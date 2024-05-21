package shortcodes

import (
	"context"

	"clerk/model"
)

type UserLastName struct {
	user *model.User
}

func NewUserLastName(u *model.User) *UserLastName {
	return &UserLastName{
		user: u,
	}
}

func (s *UserLastName) Identifier() string {
	return "user.last_name"
}

func (s *UserLastName) Substitute(_ context.Context) (any, error) {
	return s.user.LastName, nil
}
