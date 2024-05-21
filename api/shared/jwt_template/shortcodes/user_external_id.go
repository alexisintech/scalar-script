package shortcodes

import (
	"context"

	"clerk/model"
)

type UserExternalID struct {
	user *model.User
}

func NewUserExternalID(u *model.User) *UserExternalID {
	return &UserExternalID{
		user: u,
	}
}

func (s *UserExternalID) Identifier() string {
	return "user.external_id"
}

func (s *UserExternalID) Substitute(_ context.Context) (any, error) {
	return s.user.ExternalID, nil
}
