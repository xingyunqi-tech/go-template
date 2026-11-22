// Package worker holds the River workers and periodic jobs of the worker process.
package worker

import (
	"log/slog"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"

	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
)

// Deps are what the workers use.
type Deps struct {
	Schema string
	Log    *slog.Logger
}

// Config returns the River configuration of the worker process and the number of workers it
// registers; otelriver records the job metrics and spans.
func Config(d *Deps) (cfg *river.Config, workers int) {
	cfg = postgres.RiverConfig(d.Schema, d.Log)
	cfg.Workers = river.NewWorkers()
	cfg.Queues = map[string]river.QueueConfig{
		river.QueueDefault: {MaxWorkers: 1}, //nolint:exhaustruct // defaults
	}
	cfg.Middleware = []rivertype.Middleware{otelriver.NewMiddleware(nil)}

	return cfg, 0
}
