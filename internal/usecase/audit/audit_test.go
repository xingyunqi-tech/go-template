package audit_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/audit"
)

func TestSanitizeRedactsSensitiveKeysAtAnyDepth(t *testing.T) {
	t.Parallel()

	got := audit.Sanitize(map[string]any{
		"Password": "x", "hash": "h", "hashtag": "keep", "apiKey": "k", "name": "n",
		"nested": map[string]any{"refreshToken": "t", "list": []any{map[string]any{"secretValue": 1, "v": 2}}},
	})

	assert.Equal(t, map[string]any{
		"Password": "[REDACTED]", "hash": "[REDACTED]", "hashtag": "keep", "apiKey": "[REDACTED]", "name": "n",
		"nested": map[string]any{"refreshToken": "[REDACTED]", "list": []any{map[string]any{"secretValue": "[REDACTED]", "v": 2}}},
	}, got)
	assert.Nil(t, audit.Sanitize(nil))
}

func TestSanitizeTruncatesDeepValues(t *testing.T) {
	t.Parallel()

	deep := map[string]any{"v": 1}
	for range 12 {
		deep = map[string]any{"child": deep}
	}

	got := audit.Sanitize(deep)
	for range 11 {
		got, _ = got["child"].(map[string]any)
	}

	assert.Equal(t, map[string]any{"__truncated": true}, got)
}

type store struct {
	logs    []repo.AuditLog
	entries []repo.AuditEntry
	page    *repo.AuditPage
	before  time.Time
	err     error
}

func (s *store) Create(_ context.Context, l *repo.AuditLog) error {
	s.logs = append(s.logs, *l)

	return s.err
}

func (s *store) List(_ context.Context, p *repo.AuditPage) ([]repo.AuditEntry, error) {
	s.page = p

	return s.entries, s.err
}

func (s *store) DeleteBefore(_ context.Context, before time.Time) (int64, error) {
	s.before = before

	return 3, s.err
}

func TestLogSanitizesAndReturnsErrors(t *testing.T) {
	t.Parallel()

	s := &store{err: errors.New("db down")}
	err := audit.New(s).Log(t.Context(), &repo.AuditLog{Action: "note.update", Data: map[string]any{"token": "t"}})

	require.Error(t, err)
	assert.Equal(t, map[string]any{"token": "[REDACTED]"}, s.logs[0].Data)
}

func entry(at time.Time) repo.AuditEntry {
	return repo.AuditEntry{AuditLog: repo.AuditLog{Action: "note.create"}, ID: uuid.NewV7(), CreatedAt: at}
}

func TestListPagesNewestFirstWithACursor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC)
	s := &store{entries: []repo.AuditEntry{entry(now), entry(now.Add(-time.Second)), entry(now.Add(-2 * time.Second))}}
	svc := audit.New(s)

	page, err := svc.List(t.Context(), &usecase.AuditQuery{Limit: 2})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	require.NotNil(t, page.NextCursor)
	assert.Equal(t, 3, s.page.Limit)
	assert.Nil(t, s.page.AfterID)

	_, err = svc.List(t.Context(), &usecase.AuditQuery{Limit: 2, Cursor: *page.NextCursor})
	require.NoError(t, err)
	assert.Equal(t, page.Items[1].ID, *s.page.AfterID)
	assert.True(t, page.Items[1].CreatedAt.Equal(*s.page.AfterCreatedAt))

	_, err = svc.List(t.Context(), &usecase.AuditQuery{Limit: 2, Cursor: "not-a-cursor"})

	var verr *entity.ValidationError

	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "query.cursor", verr.Errors[0].Field)
}

func TestPruneDeletesBeforeTheGivenTime(t *testing.T) {
	t.Parallel()

	s := &store{}
	before := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	n, err := audit.New(s).Prune(t.Context(), before)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.Equal(t, before, s.before)
}
