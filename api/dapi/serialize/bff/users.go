package bff

import (
	"context"
	"fmt"

	"clerk/api/dapi/serializable"
	"clerk/pkg/time"

	"github.com/clerk/clerk-sdk-go/v2/user"
)

const UserObjectName = "user"

type UserResponse struct {
	ID                  string  `json:"id"`
	Object              string  `json:"object"`
	Username            *string `json:"username"`
	Name                *string `json:"name"`
	ImageURL            string  `json:"image_url,omitempty"`
	PrimaryEmailAddress *string `json:"primary_email_address"`
	PrimaryPhoneNumber  *string `json:"primary_phone_number"`
	PrimaryWeb3Wallet   *string `json:"primary_web3_wallet"`
	Identifier          string  `json:"identifier"`
	LastSignInAt        *int64  `json:"last_sign_in_at"`
	Banned              bool    `json:"banned"`
	Locked              bool    `json:"locked"`
	CreatedAt           int64   `json:"created_at"`
}

type ListUsersResponse struct {
	Users      []*UserResponse `json:"users"`
	TotalCount int64           `json:"total_count"`
}

func ListUsers(_ context.Context, users []*serializable.RowUserSerializable, totalCountResponse *user.TotalCount) *ListUsersResponse {
	userResponses := make([]*UserResponse, len(users))

	for i, user := range users {
		userResponses[i] = User(user)
	}

	return &ListUsersResponse{
		Users:      userResponses,
		TotalCount: totalCountResponse.TotalCount,
	}
}

func User(user *serializable.RowUserSerializable) *UserResponse {
	userResStruct := UserResponse{
		ID:                  user.User.ID,
		Object:              UserObjectName,
		Banned:              user.User.Banned,
		CreatedAt:           time.UnixMilli(user.User.CreatedAt),
		Username:            user.Username,
		ImageURL:            user.ImageURL,
		PrimaryEmailAddress: user.PrimaryEmailAddress,
		PrimaryPhoneNumber:  user.PrimaryPhoneNumber,
		Locked:              user.Locked,
		PrimaryWeb3Wallet:   user.PrimaryWeb3Wallet,
		Identifier:          user.Identifier,
	}

	if user.User.FirstName.Valid && user.User.LastName.Valid {
		name := fmt.Sprintf("%s %s", user.User.FirstName.String, user.User.LastName.String)
		userResStruct.Name = &name
	}

	if user.User.LastSignInAt.Valid {
		lastSignIn := time.UnixMilli(user.User.LastSignInAt.Time)
		userResStruct.LastSignInAt = &lastSignIn
	}

	return &userResStruct
}
