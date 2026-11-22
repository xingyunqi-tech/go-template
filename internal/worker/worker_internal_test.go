package worker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
)

func TestBackoffDoubles(t *testing.T) {
	t.Parallel()

	b := postgres.Backoff{Base: time.Second}
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		[]time.Duration{b.Delay(1), b.Delay(2), b.Delay(3)})
}

func TestConfigUsesTheServiceSchema(t *testing.T) {
	t.Parallel()

	cfg, workers := Config(&Deps{Schema: "svc", Log: slog.New(slog.DiscardHandler)})

	assert.Zero(t, workers)

	assert.Equal(t, "svc", cfg.Schema)
	assert.Contains(t, cfg.Queues, river.QueueDefault)
	assert.Len(t, cfg.Middleware, 1)
}
