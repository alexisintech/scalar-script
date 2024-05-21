package serialize

import (
	"clerk/model"
	"clerk/pkg/constants"
	"clerk/pkg/time"
)

const RoleObjectName = "role"

type RoleResponse struct {
	Object            string                `json:"object"`
	ID                string                `json:"id"`
	Name              string                `json:"name"`
	Key               string                `json:"key"`
	Description       string                `json:"description"`
	Permissions       []*PermissionResponse `json:"permissions"`
	IsCreatorEligible bool                  `json:"is_creator_eligible"`
	CreatedAt         int64                 `json:"created_at"`
	UpdatedAt         int64                 `json:"updated_at"`
}

func Role(role *model.Role, permissions model.Permissions) *RoleResponse {
	response := &RoleResponse{
		Object:            RoleObjectName,
		ID:                role.ID,
		Name:              role.Name,
		Key:               role.Key,
		Description:       role.Description,
		Permissions:       make([]*PermissionResponse, 0),
		IsCreatorEligible: constants.MinRequiredOrgPermissions.IsSubset(permissions.Keys()),
		CreatedAt:         time.UnixMilli(role.CreatedAt),
		UpdatedAt:         time.UnixMilli(role.UpdatedAt),
	}

	for _, permission := range permissions {
		response.Permissions = append(response.Permissions, Permission(permission))
	}

	return response
}
