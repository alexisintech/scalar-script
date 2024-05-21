package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

// ObjectOAuthApplication is the name for oauth application objects.
const ObjectOAuthApplication = "oauth_application"

// OAuthApplicationResponse is the default serialization representation
// for an oauth application object.
type OAuthApplicationResponse struct {
	Object        string `json:"object"`
	ID            string `json:"id"`
	InstanceID    string `json:"instance_id"`
	Name          string `json:"name"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret,omitempty" logger:"omit"`
	Public        bool   `json:"public"`
	Scopes        string `json:"scopes"`
	CallbackURL   string `json:"callback_url"`
	AuthorizeURL  string `json:"authorize_url"`
	TokenFetchURL string `json:"token_fetch_url"`
	UserInfoURL   string `json:"user_info_url"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// OAuthApplication will return a default serialization object
// for the provided model.OAuthApplication.
func OAuthApplication(oa *model.OAuthApplication, domain *model.Domain) *OAuthApplicationResponse {
	response := &OAuthApplicationResponse{
		Object:        ObjectOAuthApplication,
		ID:            oa.ID,
		InstanceID:    oa.InstanceID,
		Name:          oa.Name,
		ClientID:      oa.ClientID,
		Public:        oa.Public,
		Scopes:        oa.Scopes,
		CallbackURL:   oa.CallbackURL,
		AuthorizeURL:  domain.OAuthAuthorizeURL(),
		TokenFetchURL: domain.OAuthTokenURL(),
		UserInfoURL:   domain.OAuthUserInfoURL(),
		CreatedAt:     time.UnixMilli(oa.CreatedAt),
		UpdatedAt:     time.UnixMilli(oa.UpdatedAt),
	}

	if !oa.Public {
		response.ClientSecret = oa.ClientSecret
	}

	return response
}
