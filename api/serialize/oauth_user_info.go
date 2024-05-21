package serialize

import (
	"clerk/model"
	"encoding/json"
)

const ObjectOAuthUserInfo = "oauth_user_info"

type OAuthUserInfoResponse struct {
	Object          string          `json:"object"`
	InstanceID      string          `json:"instance_id"`
	Email           string          `json:"email"`
	EmailVerified   bool            `json:"email_verified"`
	FamilyName      string          `json:"family_name"`
	GivenName       string          `json:"given_name"`
	Name            string          `json:"name"`
	Username        string          `json:"username"`
	Picture         string          `json:"picture"`
	UserID          string          `json:"user_id"`
	PublicMetadata  json.RawMessage `json:"public_metadata" logger:"omit"`
	PrivateMetadata json.RawMessage `json:"private_metadata,omitempty" logger:"omit"`
	UnsafeMetadata  json.RawMessage `json:"unsafe_metadata,omitempty" logger:"omit"`
}

func OAuthUserInfo(info model.OAuthUserInfo) *OAuthUserInfoResponse {
	return &OAuthUserInfoResponse{
		Object:          ObjectOAuthUserInfo,
		InstanceID:      info.InstanceID,
		Email:           info.Email,
		EmailVerified:   info.EmailVerified,
		FamilyName:      info.FamilyName,
		GivenName:       info.GivenName,
		Name:            info.Name,
		Username:        info.Username,
		Picture:         info.Picture,
		UserID:          info.UserID,
		PublicMetadata:  info.PublicMetadata,
		PrivateMetadata: info.PrivateMetadata,
		UnsafeMetadata:  info.UnsafeMetadata,
	}
}
