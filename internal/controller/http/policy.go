package http

import "github.com/xingyunqi-tech/go-template/internal/entity"

// Auth is how an operation authenticates.
type Auth int

// Auth kinds.
const (
	AuthNone Auth = iota
	AuthJWT
	AuthOptionalJWT
	AuthRefresh
	AuthMonitor
)

func (a Auth) String() string {
	switch a {
	case AuthNone:
		return "none"
	case AuthJWT:
		return "jwt"
	case AuthOptionalJWT:
		return "optional-jwt"
	case AuthRefresh:
		return "refresh"
	case AuthMonitor:
		return "monitor-token"
	default:
		return "unknown"
	}
}

// Throttle overrides the default rate limit (nestjs @Throttle({default: {limit, ttl}}), ttl in ms).
type Throttle struct {
	Limit int
	TTL   int
}

// Policy is the per-operation behavior that nestjs declares with decorators.
type Policy struct {
	Auth Auth
	// RBAC routes go through the permission guard; Permissions are all required, or any one of
	// them when AnyPermission is set.
	RBAC          bool
	Permissions   []string
	AnyPermission bool
	// Query is the nestjs DTO validating the query string.
	Query    string
	Throttle *Throttle
	Audit    string
	Tx       bool
	// Raw responses skip the envelope.
	Raw bool
}

// Policies returns the policy of every implemented operation, keyed by operationId.
func Policies() map[string]Policy {
	users := func(permission string, tx bool) Policy {
		return Policy{Auth: AuthJWT, RBAC: true, Permissions: []string{permission}, Tx: tx}
	}

	return map[string]Policy{
		"getAppInfo":          {Auth: AuthNone},
		"simpleHealthCheck":   {Auth: AuthNone},
		"loginWithEmail":      {Auth: AuthNone, Throttle: &Throttle{Limit: 5, TTL: 60_000}, Tx: true},
		"registerWithEmail":   {Auth: AuthNone, Tx: true},
		"confirmEmail":        {Auth: AuthNone, Tx: true},
		"confirmNewEmail":     {Auth: AuthNone, Tx: true},
		"forgotPassword":      {Auth: AuthNone, Throttle: &Throttle{Limit: 3, TTL: 300_000}, Tx: true},
		"resetPassword":       {Auth: AuthNone, Throttle: &Throttle{Limit: 5, TTL: 300_000}, Tx: true},
		"getCurrentUser":      {Auth: AuthJWT},
		"updateCurrentUser":   {Auth: AuthJWT, Throttle: &Throttle{Limit: 10, TTL: 60_000}, Tx: true},
		"deleteCurrentUser":   {Auth: AuthJWT, Tx: true},
		"refreshToken":        {Auth: AuthRefresh, Tx: true},
		"logout":              {Auth: AuthJWT, Tx: true},
		"changePassword":      {Auth: AuthJWT, Throttle: &Throttle{Limit: 3, TTL: 60_000}, Tx: true},
		"getMySessions":       {Auth: AuthJWT},
		"revokeMySession":     {Auth: AuthJWT, Tx: true},
		"revokeOtherSessions": {Auth: AuthJWT, Tx: true},
		"createUser":          users(entity.PermUserCreate, true),
		"getUsers":            {Auth: AuthJWT, RBAC: true, Permissions: []string{entity.PermUserList}, Query: "QueryUserDto"},
		"getUser":             users(entity.PermUserRead, false),
		"updateUser":          users(entity.PermUserUpdate, true),
		"deleteUser":          users(entity.PermUserDelete, true),
	}
}
