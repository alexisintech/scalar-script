package shortcodes

import (
	"context"

	"clerk/model"
)

type UserFirstName struct {
	user *model.User
}

func NewUserFirstName(u *model.User) *UserFirstName {
	return &UserFirstName{
		user: u,
	}
}

func (s *UserFirstName) Identifier() string {
	return "user.first_name"
}

func (s *UserFirstName) Substitute(_ context.Context) (any, error) {
	return s.user.FirstName, nil
}
