package shortcodes

import (
	"context"

	"clerk/model"
)

type UserFullName struct {
	user *model.User
}

func NewUserFullName(u *model.User) *UserFullName {
	return &UserFullName{
		user: u,
	}
}

func (s *UserFullName) Identifier() string {
	return "user.full_name"
}

func (s *UserFullName) Substitute(_ context.Context) (any, error) {
	n := s.user.Name()
	if n != "" {
		return n, nil
	}
	return nil, nil
}
