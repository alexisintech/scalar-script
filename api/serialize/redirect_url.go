package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const RedirectURLName = "redirect_url"

type RedirectURLResponse struct {
	Object    string `json:"object"`
	ID        string `json:"id"`
	URL       string `json:"url"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

func RedirectURL(t *model.RedirectURL) *RedirectURLResponse {
	return &RedirectURLResponse{
		Object:    RedirectURLName,
		ID:        t.ID,
		URL:       t.URL,
		CreatedAt: time.UnixMilli(t.CreatedAt),
		UpdatedAt: time.UnixMilli(t.UpdatedAt),
	}
}
