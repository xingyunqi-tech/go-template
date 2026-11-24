// Package audit writes audit log entries with sensitive fields redacted, lists them and prunes
// those past AUDIT_RETENTION.
package audit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
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

var _ usecase.Audits = (*Service)(nil)

// _cursorLayout keeps the microseconds PostgreSQL stores.
const _cursorLayout = time.RFC3339Nano

// List implements usecase.Audits: newest first.
func (s *Service) List(ctx context.Context, q *usecase.AuditQuery) (pagination.CursorPage[usecase.AuditEntry], error) {
	page := &repo.AuditPage{ActorSub: q.ActorSub, Action: q.Action, AfterCreatedAt: nil, AfterID: nil, Limit: q.Limit + 1}

	if q.Cursor != "" {
		after, id, err := decodeCursor(q.Cursor)
		if err != nil {
			return pagination.CursorPage[usecase.AuditEntry]{}, err
		}

		page.AfterCreatedAt, page.AfterID = &after, &id
	}

	rows, err := s.audits.List(ctx, page)
	if err != nil {
		return pagination.CursorPage[usecase.AuditEntry]{}, err //nolint:wrapcheck // repo errors are wrapped
	}

	entries := make([]usecase.AuditEntry, 0, len(rows))
	for i := range rows {
		r := &rows[i]
		entries = append(entries, usecase.AuditEntry{
			ID: r.ID, ActorSub: r.ActorSub, Action: r.Action, ResourceType: r.ResourceType, ResourceID: r.ResourceID,
			RequestID: r.RequestID, IP: r.IP, Data: r.Data, CreatedAt: r.CreatedAt,
		})
	}

	return pagination.NewCursorPage(entries, q.Limit, func(e usecase.AuditEntry) pagination.Cursor {
		return pagination.Cursor{Key: e.CreatedAt.UTC().Format(_cursorLayout), ID: e.ID.String()}
	}), nil
}

func decodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	invalid := entity.Invalid("query.cursor", entity.RuleFormat, map[string]any{"format": "cursor"})

	c, err := pagination.DecodeCursor(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil(), invalid
	}

	after, err := time.Parse(_cursorLayout, c.Key)
	if err != nil {
		return time.Time{}, uuid.Nil(), errors.Join(invalid, err)
	}

	id, err := uuid.Parse(c.ID)
	if err != nil {
		return time.Time{}, uuid.Nil(), errors.Join(invalid, err)
	}

	return after, id, nil
}

// Prune deletes the audit logs created before before.
func (s *Service) Prune(ctx context.Context, before time.Time) (int64, error) {
	return s.audits.DeleteBefore(ctx, before) //nolint:wrapcheck // repo errors are wrapped
}
