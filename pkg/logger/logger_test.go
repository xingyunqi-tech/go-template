package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/logger"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := map[string]slog.Level{
		"trace": logger.LevelTrace,
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"fatal": logger.LevelFatal,
		"":      slog.LevelInfo,
	}
	for in, want := range tests {
		assert.Equal(t, want, logger.ParseLevel(in), in)
	}
}

func TestNewJSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	log := logger.New(&buf, "warn", false)
	log.Info("dropped")
	log.Warn("kept", "k", "v")

	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "kept", line["msg"])
	assert.Equal(t, "v", line["k"])
}

func TestNewSilent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger.New(&buf, "silent", true).Error("dropped")
	assert.Zero(t, buf.Len())
}

func TestNewPretty(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	logger.New(&buf, "debug", true).Debug("hello")
	assert.Contains(t, buf.String(), "msg=hello")
}

type ctxKey struct{}

func TestContextAttributesAreAdded(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	log := logger.New(&buf, "info", false, func(ctx context.Context) []slog.Attr {
		if v, ok := ctx.Value(ctxKey{}).(string); ok {
			return []slog.Attr{slog.String("request_id", v)}
		}

		return nil
	})

	log.With("a", 1).InfoContext(context.WithValue(t.Context(), ctxKey{}, "r-1"), "hello")
	log.Info("plain")

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.Len(t, lines, 2)

	var first, second map[string]any
	require.NoError(t, json.Unmarshal(lines[0], &first))
	require.NoError(t, json.Unmarshal(lines[1], &second))
	assert.Equal(t, "r-1", first["request_id"])
	assert.InDelta(t, 1, first["a"], 0)
	assert.NotContains(t, second, "request_id")
}
