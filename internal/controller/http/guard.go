package http

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
)

const (
	_userAgentMaxLength  = 512
	_forbiddenResource   = "Forbidden resource"
	_authorizationFields = 3
)

var _authHeader = regexp.MustCompile(`^(\S+)\s+(\S+)`)

// Authenticator validates access and refresh tokens.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*auth.Principal, error)
	AuthenticateRefresh(token string) (*auth.RefreshPrincipal, error)
}

// PermissionSource lists a role's permissions.
type PermissionSource interface {
	ForRole(ctx context.Context, roleID int) ([]string, error)
}

// bearer extracts the token like passport-jwt fromAuthHeaderAsBearerToken.
func bearer(r *http.Request) string {
	m := _authHeader.FindStringSubmatch(r.Header.Get("Authorization"))
	if len(m) < 3 || !strings.EqualFold(m[1], "bearer") {
		return ""
	}

	return m[2]
}

// AuthGuards returns the guards for the access-token, optional and refresh-token routes.
func AuthGuards(a Authenticator) map[Auth]Guard {
	return map[Auth]Guard{
		AuthJWT: func(r *http.Request) (*http.Request, error) {
			token := bearer(r)
			if token == "" {
				return r, entity.Unauthorized("")
			}

			p, err := a.Authenticate(r.Context(), token)
			if err != nil {
				return r, err //nolint:wrapcheck // auth errors are HTTP errors
			}

			return r.WithContext(reqctx.WithPrincipal(r.Context(), p)), nil
		},
		AuthOptionalJWT: func(r *http.Request) (*http.Request, error) {
			token := bearer(r)
			if token == "" {
				return r, nil
			}

			p, err := a.Authenticate(r.Context(), token)
			if err != nil {
				return r, nil //nolint:nilerr // an invalid optional token means anonymous
			}

			return r.WithContext(reqctx.WithPrincipal(r.Context(), p)), nil
		},
		AuthRefresh: func(r *http.Request) (*http.Request, error) {
			token := bearer(r)
			if token == "" {
				return r, entity.Unauthorized("")
			}

			p, err := a.AuthenticateRefresh(token)
			if err != nil {
				return r, err //nolint:wrapcheck // auth errors are HTTP errors
			}

			return r.WithContext(reqctx.WithRefresh(r.Context(), p)), nil
		},
	}
}

// MonitorGuard is nestjs MonitoringTokenGuard: with a token configured the request must carry
// Authorization: Bearer <token>; without one only non-production environments are open.
func MonitorGuard(token string, production bool) Guard {
	return func(r *http.Request) (*http.Request, error) {
		if monitorAllowed(r.Header.Get("Authorization"), token, production) {
			return r, nil
		}

		return r, entity.Forbidden(_forbiddenResource)
	}
}

func monitorAllowed(authorization, token string, production bool) bool {
	if token == "" {
		return !production
	}

	scheme, credentials := authorizationParts(authorization)

	return strings.EqualFold(scheme, "bearer") && subtle.ConstantTimeCompare([]byte(credentials), []byte(token)) == 1
}

// authorizationParts splits Authorization like `header.trim().split(/\s+/)`.
func authorizationParts(authorization string) (scheme, credentials string) {
	parts := _whitespace.Split(strings.TrimSpace(authorization), _authorizationFields)
	if len(parts) > 1 {
		credentials = parts[1]
	}

	return parts[0], credentials
}

// RequirePermissions is the nestjs PermissionsGuard.
func RequirePermissions(source PermissionSource) PermissionGuard {
	return func(r *http.Request, policy Policy) error {
		if len(policy.Permissions) == 0 {
			return entity.Forbidden("No permission declared for this route")
		}

		p := reqctx.Principal(r.Context())
		if p == nil {
			return entity.Forbidden("User not authenticated")
		}

		if p.RoleID == nil {
			return entity.Forbidden("User role not found")
		}

		granted, err := source.ForRole(r.Context(), *p.RoleID)
		if err != nil {
			return err //nolint:wrapcheck // permission errors are wrapped
		}

		return checkPermissions(granted, policy.Permissions, policy.AnyPermission)
	}
}

func checkPermissions(granted, required []string, anyOf bool) error {
	has := make(map[string]bool, len(granted))
	for _, g := range granted {
		has[g] = true
	}

	var missing []string

	for _, need := range required {
		if !has[need] {
			missing = append(missing, need)
		}
	}

	switch {
	case anyOf && len(missing) == len(required):
		return entity.Forbidden("Missing any of permissions: " + strings.Join(required, ", "))
	case !anyOf && len(missing) > 0:
		return entity.Forbidden("Missing permissions: " + strings.Join(missing, ", "))
	default:
		return nil
	}
}

// clientIP is Express req.ip with `trust proxy` set to a hop count: walk back hops entries from
// the direct peer through X-Forwarded-For.
func clientIP(r *http.Request, trustProxy int) *string {
	var chain []string

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		chain = append(chain, host)
	} else if r.RemoteAddr != "" {
		chain = append(chain, r.RemoteAddr)
	}

	if forwarded := r.Header.Get("X-Forwarded-For"); trustProxy > 0 && forwarded != "" {
		parts := strings.Split(forwarded, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			if p := strings.TrimSpace(parts[i]); p != "" {
				chain = append(chain, p)
			}
		}
	}

	if len(chain) == 0 {
		return nil
	}

	ip := chain[min(trustProxy, len(chain)-1)]

	return &ip
}

// truncateUTF16 keeps at most limit UTF-16 code units, like String.prototype.slice(0, limit),
// without splitting a surrogate pair.
func truncateUTF16(s string, limit int) string {
	units := 0

	for i, r := range s {
		size := utf16.RuneLen(r)

		if units+size > limit {
			return s[:i]
		}

		units += size
	}

	return s
}

// clientInfo stores the user agent (at most 512 UTF-16 code units) and client IP.
func clientInfo(trustProxy int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var agent *string

			if values := r.Header.Values("User-Agent"); len(values) > 0 {
				a := truncateUTF16(values[0], _userAgentMaxLength)
				agent = &a
			}

			ctx := reqctx.WithClient(r.Context(), auth.Client{UserAgent: agent, IP: clientIP(r, trustProxy)})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
