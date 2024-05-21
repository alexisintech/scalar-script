package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const BackupCodeObjectName = "backup_code"

type BackupCodeResponse struct {
	Object    string   `json:"object"`
	ID        string   `json:"id"`
	Codes     []string `json:"codes"`
	CreatedAt int64    `json:"created_at"`
	UpdatedAt int64    `json:"updated_at"`
}

func BackupCode(bc *model.BackupCode, plainCodes []string) *BackupCodeResponse {
	return &BackupCodeResponse{
		Object:    BackupCodeObjectName,
		ID:        bc.ID,
		Codes:     plainCodes,
		CreatedAt: time.UnixMilli(bc.CreatedAt),
		UpdatedAt: time.UnixMilli(bc.UpdatedAt),
	}
}
