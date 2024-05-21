package serialize

import (
	"net/url"

	"clerk/model"
	"clerk/pkg/time"
)

const SignInTokenObjectName = "sign_in_token"

type SignInTokenResponse struct {
	Object    string `json:"object"`
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Token     string `json:"token,omitempty"`
	Status    string `json:"status"`
	URL       string `json:"url,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func SignInToken(signInToken *model.SignInToken, ticketURL *url.URL, ticket string) *SignInTokenResponse {
	res := &SignInTokenResponse{
		Object:    SignInTokenObjectName,
		ID:        signInToken.ID,
		UserID:    signInToken.UserID,
		Token:     ticket,
		Status:    signInToken.Status,
		CreatedAt: time.UnixMilli(signInToken.CreatedAt),
		UpdatedAt: time.UnixMilli(signInToken.UpdatedAt),
	}
	if ticketURL != nil {
		res.URL = ticketURL.String()
	}

	return res
}
