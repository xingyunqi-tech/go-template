package http

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
	return map[string]Policy{
		"getAppInfo":        {Auth: AuthNone},
		"simpleHealthCheck": {Auth: AuthNone},
	}
}
