package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo"
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
	logs []repo.AuditLog
	err  error
}

func (s *store) Create(_ context.Context, l *repo.AuditLog) error {
	s.logs = append(s.logs, *l)

	return s.err
}

func TestLogSanitizesAndReturnsErrors(t *testing.T) {
	t.Parallel()

	s := &store{err: errors.New("db down")}
	err := audit.New(s).Log(t.Context(), &repo.AuditLog{Action: "note.update", Data: map[string]any{"token": "t"}})

	require.Error(t, err)
	assert.Equal(t, map[string]any{"token": "[REDACTED]"}, s.logs[0].Data)
}
