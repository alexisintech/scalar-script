package constants

import "clerk/pkg/set"

// OrgDashboardPermissionPrefix is the reserved prefix for Clerk dashboard permission keys
const OrgDashboardPermissionPrefix = "org:dashboard_"

const (
	DashboardPermissionUsersImpersonate = OrgDashboardPermissionPrefix + "users:impersonate"
	DashboardPermissionUsersRead        = OrgDashboardPermissionPrefix + "users:read"
	DashboardPermissionUsersCreate      = OrgDashboardPermissionPrefix + "users:create"
	DashboardPermissionUsersUpdate      = OrgDashboardPermissionPrefix + "users:update"
	DashboardPermissionUsersDelete      = OrgDashboardPermissionPrefix + "users:delete"
)

const (
	DashboardRoleAdmin       = "org:admin"
	DashboardRoleBasicMember = "org:member"
	DashboardRoleSupport     = "org:support"
)

var (
	DashboardAdminPermissions = set.New(
		DashboardPermissionUsersImpersonate, DashboardPermissionUsersRead,
		DashboardPermissionUsersCreate, DashboardPermissionUsersUpdate,
		DashboardPermissionUsersDelete,
	)
	DashboardBasicMemberPermissions = set.New(DashboardPermissionUsersRead,
		DashboardPermissionUsersCreate, DashboardPermissionUsersUpdate,
		DashboardPermissionUsersDelete)
	DashboardSupportPermissions = set.New(
		DashboardPermissionUsersImpersonate, DashboardPermissionUsersRead,
	)
)
