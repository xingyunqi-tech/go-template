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

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// RiverSchema holds the River tables, apart from the nestjs tables.
const RiverSchema = "river"

// Attempt and uniqueness settings of the nestjs queues.
const (
	DefaultAttempts        = 3
	AccountCleanupAttempts = 5
)

// MigrateRiver creates the river schema and applies the River migrations.
func MigrateRiver(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	if _, err := pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS "`+RiverSchema+`"`); err != nil {
		return fmt.Errorf("postgres: river schema: %w", err)
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{ //nolint:exhaustruct // defaults
		Schema: RiverSchema, Logger: log,
	})
	if err != nil {
		return fmt.Errorf("postgres: river migrator: %w", err)
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

// RiverConfig is the client configuration shared by the API (insert only) and the worker.
func RiverConfig(log *slog.Logger) *river.Config {
	return &river.Config{ //nolint:exhaustruct // River defaults for the rest
		Schema:      RiverSchema,
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

// Jobs implements repo.Jobs with River; inside a request transaction the insert runs under a
// savepoint, so a failed insert leaves the transaction usable.
type Jobs struct {
	client *river.Client[pgx.Tx]
}

// NewJobs returns Jobs inserting through client.
func NewJobs(client *river.Client[pgx.Tx]) *Jobs {
	return &Jobs{client: client}
}

var _ repo.Jobs = (*Jobs)(nil)

func (j *Jobs) insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) error {
	s := stateOf(ctx)
	if s == nil {
		if _, err := j.client.Insert(ctx, args, opts); err != nil {
			return fmt.Errorf("postgres: enqueue %s: %w", args.Kind(), err)
		}

		return nil
	}

	savepoint, err := s.tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: enqueue %s: savepoint: %w", args.Kind(), err)
	}

	if _, err := j.client.InsertTx(ctx, savepoint, args, opts); err != nil {
		_ = savepoint.Rollback(ctx)

		return fmt.Errorf("postgres: enqueue %s: %w", args.Kind(), err)
	}

	if err := savepoint.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: enqueue %s: release savepoint: %w", args.Kind(), err)
	}

	return nil
}

// EnqueueMail queues a send-mail job on the mail queue.
func (j *Jobs) EnqueueMail(ctx context.Context, job entity.MailJob) error {
	return j.insert(ctx, job, &river.InsertOpts{Queue: entity.QueueMail}) //nolint:exhaustruct // defaults
}

// AccountCleanupInsertOpts dedupes per user like the nestjs jobId user-<id>: an unfinished job of
// the same user blocks the insert, a completed or discarded one does not.
func AccountCleanupInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{ //nolint:exhaustruct // defaults
		Queue:       entity.QueueAccountCleanup,
		MaxAttempts: AccountCleanupAttempts,
		UniqueOpts: river.UniqueOpts{ //nolint:exhaustruct // defaults
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
				rivertype.JobStateRunning, rivertype.JobStateScheduled,
			},
		},
	}
}

// EnqueueAccountCleanup queues a clean-account job.
func (j *Jobs) EnqueueAccountCleanup(ctx context.Context, userID int) error {
	return j.insert(ctx, entity.AccountCleanupJob{UserID: userID}, AccountCleanupInsertOpts())
}

// EnqueueNotification queues a job on the notification queue.
func (j *Jobs) EnqueueNotification(ctx context.Context, job entity.NotificationJob) error {
	return j.insert(ctx, job, &river.InsertOpts{Queue: entity.QueueNotification}) //nolint:exhaustruct // defaults
}
