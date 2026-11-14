// Package worker holds the River workers and periodic jobs of the worker process.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
	"github.com/xingyunqi-tech/go-template/internal/usecase/accountcleanup"
)

const (
	_notificationWorkers  = 5
	_cleanupLockTTL       = 10 * time.Minute
	_cleanupLockedSnooze  = 30 * time.Second
	_cleanupBackoff       = 10 * time.Second
	_sessionRetentionDays = 7
	_readRetentionDays    = 30
	_sessionPurgeHour     = 3
	_notificationHour     = 4
	_compensationMinute   = 25
)

// Counter records queue_jobs_total.
type Counter interface {
	Job(queue, status string)
}

// Deps are what the workers use.
type Deps struct {
	Composer    *mail.Composer
	Sender      mail.Sender
	Cleanup     *accountcleanup.Service
	Locks       repo.Locks
	Maintenance repo.Maintenance
	Counter     Counter
	Log         *slog.Logger
	Notify      NotificationDeps
}

// Config returns the River configuration of the worker process.
func Config(d *Deps) (*river.Config, error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &MailWorker{WorkerDefaults: river.WorkerDefaults[entity.MailJob]{}, composer: d.Composer, sender: d.Sender})
	river.AddWorker(workers, &AccountCleanupWorker{
		WorkerDefaults: river.WorkerDefaults[entity.AccountCleanupJob]{}, cleanup: d.Cleanup, locks: d.Locks,
	})
	river.AddWorker(workers, &SessionPurgeWorker{WorkerDefaults: river.WorkerDefaults[SessionPurgeArgs]{}, maintenance: d.Maintenance, log: d.Log})
	river.AddWorker(workers, &ReadNotificationsWorker{
		WorkerDefaults: river.WorkerDefaults[ReadNotificationsArgs]{}, maintenance: d.Maintenance, log: d.Log,
	})
	river.AddWorker(workers, &CompensationWorker{WorkerDefaults: river.WorkerDefaults[CompensationArgs]{}, cleanup: d.Cleanup, log: d.Log})
	addNotificationWorkers(workers, d)

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return nil, fmt.Errorf("worker: %w", err)
	}

	cfg := postgres.RiverConfig(d.Log)
	cfg.Workers = workers
	cfg.Queues = map[string]river.QueueConfig{
		entity.QueueMail:           {MaxWorkers: 1},                    //nolint:exhaustruct // defaults
		entity.QueueNotification:   {MaxWorkers: _notificationWorkers}, //nolint:exhaustruct // defaults
		entity.QueueAccountCleanup: {MaxWorkers: 1},                    //nolint:exhaustruct // defaults
		entity.QueueScheduler:      {MaxWorkers: 1},                    //nolint:exhaustruct // defaults
	}
	cfg.Middleware = []rivertype.Middleware{&jobMetrics{MiddlewareDefaults: river.MiddlewareDefaults{}, counter: d.Counter}}
	cfg.PeriodicJobs = []*river.PeriodicJob{
		periodic(daily{hour: _sessionPurgeHour, minute: 0, loc: time.UTC}, SessionPurgeArgs{}),
		periodic(daily{hour: _notificationHour, minute: 0, loc: time.UTC}, ReadNotificationsArgs{}),
		periodic(hourly{minute: _compensationMinute, loc: shanghai}, CompensationArgs{}),
	}

	return cfg, nil
}

func periodic(schedule river.PeriodicSchedule, args river.JobArgs) *river.PeriodicJob {
	return river.NewPeriodicJob(schedule, func() (river.JobArgs, *river.InsertOpts) {
		return args, &river.InsertOpts{Queue: entity.QueueScheduler, MaxAttempts: 1} //nolint:exhaustruct // defaults
	}, &river.PeriodicJobOpts{ID: args.Kind()}) //nolint:exhaustruct // defaults
}

// jobMetrics counts every finished attempt as completed or failed.
type jobMetrics struct {
	river.MiddlewareDefaults

	counter Counter
}

func (m *jobMetrics) Work(ctx context.Context, job *rivertype.JobRow, doInner func(context.Context) error) error {
	err := doInner(ctx)

	var snooze *river.JobSnoozeError

	switch {
	case err == nil:
		m.counter.Job(job.Queue, "completed")
	case !errors.As(err, &snooze):
		m.counter.Job(job.Queue, "failed")
	}

	return err
}

// MailWorker renders and sends a queued mail.
type MailWorker struct {
	river.WorkerDefaults[entity.MailJob]

	composer *mail.Composer
	sender   mail.Sender
}

// Work implements river.Worker.
func (w *MailWorker) Work(ctx context.Context, job *river.Job[entity.MailJob]) error {
	out, err := w.composer.Compose(&mail.Message{
		Template: job.Args.Template, To: job.Args.To, Data: job.Args.Data, Language: job.Args.Language,
	})
	if err != nil {
		return err //nolint:wrapcheck // composer errors are wrapped
	}

	return w.sender.Send(ctx, out) //nolint:wrapcheck // sender errors are wrapped
}

// AccountCleanupWorker runs the cleaners of one user while holding its lock; the last failed
// attempt raises an alert.
type AccountCleanupWorker struct {
	river.WorkerDefaults[entity.AccountCleanupJob]

	cleanup *accountcleanup.Service
	locks   repo.Locks
}

// NextRetry waits 10s × 2^(attempt-1).
func (w *AccountCleanupWorker) NextRetry(job *river.Job[entity.AccountCleanupJob]) time.Time {
	return time.Now().Add(postgres.Backoff{Base: _cleanupBackoff}.Delay(job.Attempt))
}

// Timeout matches the cleanup lock TTL.
func (w *AccountCleanupWorker) Timeout(*river.Job[entity.AccountCleanupJob]) time.Duration {
	return _cleanupLockTTL
}

// Work implements river.Worker.
func (w *AccountCleanupWorker) Work(ctx context.Context, job *river.Job[entity.AccountCleanupJob]) error {
	key := "account-cleanup:user-" + strconv.Itoa(job.Args.UserID)

	token, ok := w.locks.TryAcquire(ctx, key, _cleanupLockTTL)
	if !ok {
		return river.JobSnooze(_cleanupLockedSnooze)
	}
	defer w.locks.Release(context.WithoutCancel(ctx), key, token)

	err := w.cleanup.Run(ctx, job.Args.UserID)
	if err != nil && job.Attempt >= job.MaxAttempts {
		w.cleanup.ReportFailure(ctx, job.Args.UserID, err)
	}

	return err //nolint:wrapcheck // cleanup errors are reported as they are
}

// SessionPurgeArgs is the daily purge of old soft-deleted sessions.
type SessionPurgeArgs struct{}

// Kind implements river.JobArgs.
func (SessionPurgeArgs) Kind() string { return "cleanup-old-sessions" }

// SessionPurgeWorker hard-deletes sessions soft-deleted over 7 days ago.
type SessionPurgeWorker struct {
	river.WorkerDefaults[SessionPurgeArgs]

	maintenance repo.Maintenance
	log         *slog.Logger
}

// Work implements river.Worker.
func (w *SessionPurgeWorker) Work(ctx context.Context, _ *river.Job[SessionPurgeArgs]) error {
	n, err := w.maintenance.PurgeDeletedSessions(ctx, _sessionRetentionDays)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	w.log.InfoContext(ctx, "old sessions permanently deleted", "count", n)

	return nil
}

// ReadNotificationsArgs is the daily cleanup of old read notifications.
type ReadNotificationsArgs struct{}

// Kind implements river.JobArgs.
func (ReadNotificationsArgs) Kind() string { return "cleanup-read-notifications" }

// ReadNotificationsWorker soft-deletes notifications read over 30 days ago.
type ReadNotificationsWorker struct {
	river.WorkerDefaults[ReadNotificationsArgs]

	maintenance repo.Maintenance
	log         *slog.Logger
}

// Work implements river.Worker.
func (w *ReadNotificationsWorker) Work(ctx context.Context, _ *river.Job[ReadNotificationsArgs]) error {
	n, err := w.maintenance.SoftDeleteReadNotifications(ctx, _readRetentionDays)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	w.log.InfoContext(ctx, "old read notifications soft deleted", "count", n)

	return nil
}

// CompensationArgs is the hourly requeue of unfinished account cleanups.
type CompensationArgs struct{}

// Kind implements river.JobArgs.
func (CompensationArgs) Kind() string { return "account-cleanup-compensation" }

// CompensationWorker requeues the cleanups that did not complete.
type CompensationWorker struct {
	river.WorkerDefaults[CompensationArgs]

	cleanup *accountcleanup.Service
	log     *slog.Logger
}

// Work implements river.Worker.
func (w *CompensationWorker) Work(ctx context.Context, _ *river.Job[CompensationArgs]) error {
	n, err := w.cleanup.Compensate(ctx)
	if err != nil {
		return err //nolint:wrapcheck // usecase errors are wrapped
	}

	w.log.InfoContext(ctx, "account cleanups requeued", "count", n)

	return nil
}
