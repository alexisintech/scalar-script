package serialize

import (
	"clerk/model"
	"clerk/pkg/time"
)

const PermissionObjectName = "permission"

type PermissionResponse struct {
	Object      string `json:"object"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Key         string `json:"key"`
	Description string `json:"description"`
	Type        string `json:"type"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

func Permission(permission *model.Permission) *PermissionResponse {
	return &PermissionResponse{
		Object:      PermissionObjectName,
		ID:          permission.ID,
		Name:        permission.Name,
		Key:         permission.Key,
		Description: permission.Description,
		Type:        permission.Type,
		CreatedAt:   time.UnixMilli(permission.CreatedAt),
		UpdatedAt:   time.UnixMilli(permission.UpdatedAt),
	}
}
