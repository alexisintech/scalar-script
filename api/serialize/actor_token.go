package serialize

import (
	"encoding/json"
	"net/url"

	"clerk/model"
	"clerk/pkg/time"
)

const ActorTokenObjectName = "actor_token"

type ActorTokenResponse struct {
	Object    string          `json:"object"`
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Actor     json.RawMessage `json:"actor"`
	Token     string          `json:"token,omitempty"`
	URL       string          `json:"url,omitempty"`
	Status    string          `json:"status"`
	CreatedAt int64           `json:"created_at"`
	UpdatedAt int64           `json:"updated_at"`
}

func ActorToken(actorToken *model.ActorToken, tokenURL *url.URL, token string) *ActorTokenResponse {
	return &ActorTokenResponse{
		Object:    ActorTokenObjectName,
		ID:        actorToken.ID,
		UserID:    actorToken.UserID,
		Actor:     json.RawMessage(actorToken.Actor),
		Token:     token,
		URL:       tokenURL.String(),
		Status:    actorToken.Status,
		CreatedAt: time.UnixMilli(actorToken.CreatedAt),
		UpdatedAt: time.UnixMilli(actorToken.UpdatedAt),
	}
}
