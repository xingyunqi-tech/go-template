package entity

import "slices"

// Role codes (platform conventions §5.5).
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// Platform permission codes, granted to admin only.
const (
	PermUserRead  = "user:read"
	PermUserWrite = "user:write"
	PermRoleRead  = "role:read"
	PermRoleWrite = "role:write"
	PermAuditRead = "audit:read"
)

// RoleDef is a role and the permission codes it grants in this service.
type RoleDef struct {
	Code        string
	Name        string
	Permissions []string
}

// servicePermissions are the private permission codes of this service and the roles granting
// them; admin holds every one of them.
func servicePermissions() map[string][]string {
	return map[string][]string{
		// modulegen:permissions
	}
}

// Permissions lists every permission code of this service, sorted.
func Permissions() []string {
	codes := []string{PermUserRead, PermUserWrite, PermRoleRead, PermRoleWrite, PermAuditRead}

	for code := range servicePermissions() {
		codes = append(codes, code)
	}

	slices.Sort(codes)

	return codes
}

// Roles lists the roles of this service with their permission codes.
func Roles() []RoleDef {
	var userPerms []string

	for code, roles := range servicePermissions() {
		if slices.Contains(roles, RoleUser) {
			userPerms = append(userPerms, code)
		}
	}

	slices.Sort(userPerms)

	return []RoleDef{
		{Code: RoleAdmin, Name: "Administrator", Permissions: Permissions()},
		{Code: RoleUser, Name: "User", Permissions: userPerms},
	}
}

// KnownRole reports whether code is a role of this service.
func KnownRole(code string) bool {
	return code == RoleAdmin || code == RoleUser
}

// RBAC answers permission checks from the fixed role table.
type RBAC struct{}

// Granted reports whether one of roles grants permission.
func (RBAC) Granted(roles []string, permission string) bool {
	if slices.Contains(roles, RoleAdmin) {
		return slices.Contains(Permissions(), permission)
	}

	for _, role := range roles {
		if slices.Contains(servicePermissions()[permission], role) {
			return true
		}
	}

	return false
}
