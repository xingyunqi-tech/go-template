package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"
)

// DefaultAttempts is the attempt limit of a job.
const DefaultAttempts = 3

// riverMigrator migrates the River tables of schema.
func riverMigrator(pool *pgxpool.Pool, schema string, log *slog.Logger) (*rivermigrate.Migrator[pgx.Tx], error) {
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{ //nolint:exhaustruct // defaults
		Schema: schema, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: river migrator: %w", err)
	}

	return migrator, nil
}

// MigrateRiver applies the River migrations in schema.
func MigrateRiver(ctx context.Context, pool *pgxpool.Pool, schema string, log *slog.Logger) error {
	migrator, err := riverMigrator(pool, schema, log)
	if err != nil {
		return err
	}

	res, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("postgres: river migrate: %w", err)
	}

	for _, v := range res.Versions {
		log.InfoContext(ctx, "river migration applied", "version", v.Version, "duration_ms", v.Duration.Milliseconds())
	}

	return nil
}

// RollbackRiver removes the River tables of schema.
func RollbackRiver(ctx context.Context, pool *pgxpool.Pool, schema string, log *slog.Logger) error {
	migrator, err := riverMigrator(pool, schema, log)
	if err != nil {
		return err
	}

	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionDown, &rivermigrate.MigrateOpts{TargetVersion: -1}); err != nil { //nolint:exhaustruct // defaults
		return fmt.Errorf("postgres: river rollback: %w", err)
	}

	return nil
}

// RiverConfig is the client configuration shared by the API (insert only) and the worker.
func RiverConfig(schema string, log *slog.Logger) *river.Config {
	return &river.Config{ //nolint:exhaustruct // River defaults for the rest
		Schema:      schema,
		MaxAttempts: DefaultAttempts,
		Logger:      log,
		RetryPolicy: Backoff{Base: time.Second},
	}
}

// Backoff is the BullMQ exponential backoff: Base × 2^(attempt-1) after each failed attempt.
type Backoff struct {
	Base time.Duration
}

// NextRetry implements river.ClientRetryPolicy.
func (b Backoff) NextRetry(job *rivertype.JobRow) time.Time {
	return time.Now().Add(b.Delay(job.Attempt))
}

// Delay is the wait after the attempt-th failed attempt.
func (b Backoff) Delay(attempt int) time.Duration {
	return b.Base << max(attempt-1, 0)
}
