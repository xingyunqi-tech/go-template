// Package audit writes audit log entries with sensitive fields redacted.
package audit

import (
	"context"
	"slices"
	"strings"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_redacted = "[REDACTED]"
	_maxDepth = 10
)

func sensitiveParts() []string {
	return []string{"password", "secret", "token", "apikey", "privatekey", "sessionkey", "accesskey"}
}

// Sensitive reports a key whose value is redacted: "hash", or containing a sensitive part.
func Sensitive(key string) bool {
	lower := strings.ToLower(key)

	return lower == "hash" || slices.ContainsFunc(sensitiveParts(), func(p string) bool { return strings.Contains(lower, p) })
}

// Sanitize redacts sensitive keys at any depth and truncates below ten levels.
func Sanitize(v map[string]any) map[string]any {
	return sanitize(v, 0)
}

func sanitize(v map[string]any, depth int) map[string]any {
	if v == nil {
		return nil
	}

	if depth > _maxDepth {
		return map[string]any{"__truncated": true}
	}

	out := make(map[string]any, len(v))

	for k, val := range v {
		if Sensitive(k) {
			out[k] = _redacted

			continue
		}

		out[k] = sanitizeValue(val, depth)
	}

	return out
}

func sanitizeValue(v any, depth int) any {
	switch x := v.(type) {
	case map[string]any:
		return sanitize(x, depth+1)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = sanitizeValue(item, depth+1)
		}

		return out
	default:
		return v
	}
}

// Service writes audit logs.
type Service struct {
	audits repo.Audits
}

// New returns a Service.
func New(audits repo.Audits) *Service {
	return &Service{audits: audits}
}

// Log records l with its data sanitized.
func (s *Service) Log(ctx context.Context, l *repo.AuditLog) error {
	entry := *l
	entry.Data = Sanitize(l.Data)

	return s.audits.Create(ctx, &entry) //nolint:wrapcheck // repo errors are wrapped
}
