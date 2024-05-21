package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const TOTPObjectName = "totp"

type TOTPResponse struct {
	Object      string   `json:"object"`
	ID          string   `json:"id"`
	Secret      *string  `json:"secret" logger:"redact"`
	URI         *string  `json:"uri"  logger:"redact"`
	Verified    bool     `json:"verified"`
	BackupCodes []string `json:"backup_codes" logger:"redact"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
}

func TOTP(totp *model.TOTP, qrURI string) *TOTPResponse {
	return &TOTPResponse{
		Object:    TOTPObjectName,
		ID:        totp.ID,
		Secret:    &totp.Secret,
		URI:       &qrURI,
		Verified:  totp.Verified,
		CreatedAt: time.UnixMilli(totp.CreatedAt),
		UpdatedAt: time.UnixMilli(totp.UpdatedAt),
	}
}

func TOTPAttempt(totp *model.TOTP, backupCodes []string) *TOTPResponse {
	return &TOTPResponse{
		Object:      TOTPObjectName,
		ID:          totp.ID,
		Verified:    totp.Verified,
		BackupCodes: backupCodes,
		CreatedAt:   time.UnixMilli(totp.CreatedAt),
		UpdatedAt:   time.UnixMilli(totp.UpdatedAt),
	}
}
