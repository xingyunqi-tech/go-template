package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/logger"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo,
	} {
		assert.Equal(t, want, logger.ParseLevel(in), in)
	}
}

func TestJSONRecordsCarryThePlatformFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	extract := func(context.Context) []slog.Attr { return []slog.Attr{slog.String("request_id", "req-1")} }
	log := logger.New(&buf, logger.Options{Level: "debug", Text: false, Service: "svc", Env: "test"}, extract)

	log.WarnContext(t.Context(), "hello",
		"email", "alice@example.com", "phone", "+8613800000000", "password", "p", "refreshToken", "r", //nolint:sloglint // credential keys as clients send them
		slog.Group("req", "Authorization", "Bearer x", "path", "/a")) //nolint:sloglint // header name

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))

	assert.Regexp(t, regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`), got["ts"])
	assert.Equal(t, "warn", got["level"])
	assert.Equal(t, "hello", got["msg"])
	assert.Equal(t, "svc", got["service"])
	assert.Equal(t, "test", got["env"])
	assert.Equal(t, "req-1", got["request_id"])
	assert.Equal(t, "a***@example.com", got["email"])
	assert.Equal(t, "+86138****0000", got["phone"])
	assert.NotContains(t, got, "password")
	assert.NotContains(t, got, "refreshToken")
	assert.NotContains(t, got, "time")
	assert.Equal(t, map[string]any{"path": "/a"}, got["req"])
}

func TestLevelFiltersRecords(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	log := logger.New(&buf, logger.Options{Level: "warn", Text: true, Service: "svc", Env: "test"})
	log.Info("dropped")
	log.Error("kept")

	assert.NotContains(t, buf.String(), "dropped")
	assert.Contains(t, buf.String(), "kept")
	assert.Contains(t, buf.String(), "level=error")
}

func TestMasks(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a***@example.com", logger.MaskEmail("alice@example.com"))
	assert.Equal(t, "***", logger.MaskEmail("nope"))
	assert.Equal(t, "+86138****0000", logger.MaskPhone("+8613800000000"))
	assert.Equal(t, "***", logger.MaskPhone("12345"))
}
