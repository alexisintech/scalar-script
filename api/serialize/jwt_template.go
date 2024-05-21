package serialize

import (
	"encoding/json"

	"clerk/model"
	"clerk/pkg/time"
)

const JWTTemplateObjectName = "jwt_template"

type JWTTemplateResponse struct {
	Object           string          `json:"object"`
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Claims           json.RawMessage `json:"claims" logger:"omit"`
	Lifetime         int             `json:"lifetime"`
	AllowedClockSkew int             `json:"allowed_clock_skew"`
	CustomSigningKey bool            `json:"custom_signing_key" logger:"omit"`
	SigningAlgorithm string          `json:"signing_algorithm" logger:"omit"`
	CreatedAt        int64           `json:"created_at"`
	UpdatedAt        int64           `json:"updated_at"`
}

func JWTTemplate(t *model.JWTTemplate) *JWTTemplateResponse {
	r := &JWTTemplateResponse{
		Object:           JWTTemplateObjectName,
		ID:               t.ID,
		Name:             t.Name,
		Claims:           json.RawMessage(t.Claims),
		Lifetime:         t.Lifetime,
		AllowedClockSkew: t.ClockSkew,
		SigningAlgorithm: t.SigningAlgorithm,
		CreatedAt:        time.UnixMilli(t.CreatedAt),
		UpdatedAt:        time.UnixMilli(t.UpdatedAt),
	}

	if t.SigningKey.Valid {
		r.CustomSigningKey = true
	}

	return r
}
