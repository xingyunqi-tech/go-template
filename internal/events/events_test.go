package events_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/events"
)

func TestUserDeletedIsACloudEvent(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("0190a5e0-0000-7000-8000-000000000001")

	e, err := events.NewUserDeleted("svc", id, time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600)))
	require.NoError(t, err)

	var buf bytes.Buffer

	require.NoError(t, events.NewLog(slog.New(slog.NewJSONHandler(&buf, nil))).Publish(t.Context(), e))

	var line struct {
		Event string `json:"event"`
	}

	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))

	var got map[string]any

	require.NoError(t, json.Unmarshal([]byte(line.Event), &got))
	assert.Equal(t, "1.0", got["specversion"])
	assert.Equal(t, "user.deleted", got["type"])
	assert.Equal(t, "svc", got["source"])
	assert.Equal(t, "application/json", got["datacontenttype"])
	assert.Equal(t, "2026-01-02T02:04:05Z", got["time"])
	assert.Equal(t, map[string]any{"userId": id.String()}, got["data"])
	assert.NotEmpty(t, got["id"])
}
