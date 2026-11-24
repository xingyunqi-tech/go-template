// Package worker holds the River workers and periodic jobs of the worker process.
package worker

import (
	"context"
	"log/slog"
	"time"
	"uuid"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
)

const (
	_mailWorkers    = 4
	_accountWorkers = 2
	_period         = time.Hour
)

// MailDeliverer runs a mail job.
type MailDeliverer interface {
	DeliverMail(ctx context.Context, job *repo.MailJob) error
}

// AccountPurger runs the account.purge jobs and reschedules the purges left behind.
type AccountPurger interface {
	Purge(ctx context.Context, userID uuid.UUID) error
	Sweep(ctx context.Context) error
}

// Maintainer runs the periodic cleanup.
type Maintainer interface {
	Run(ctx context.Context) error
}

// Deps are what the workers use; a nil Mail, Accounts or Maintenance registers no worker for it.
type Deps struct {
	Schema      string
	Log         *slog.Logger
	Mail        MailDeliverer
	Accounts    AccountPurger
	Maintenance Maintainer
}

// periodic adds the hourly job of args, also run when a leader starts.
func periodic(cfg *river.Config, args river.JobArgs) {
	cfg.PeriodicJobs = append(cfg.PeriodicJobs, river.NewPeriodicJob(river.PeriodicInterval(_period),
		func() (river.JobArgs, *river.InsertOpts) { return args, nil },
		&river.PeriodicJobOpts{ID: args.Kind(), RunOnStart: true}))
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

	if d.Mail != nil {
		river.AddWorker(cfg.Workers, &MailWorker{mail: d.Mail})
		cfg.Queues[postgres.QueueMail] = river.QueueConfig{MaxWorkers: _mailWorkers} //nolint:exhaustruct // defaults
		workers++
	}

	if d.Accounts != nil {
		river.AddWorker(cfg.Workers, &PurgeWorker{accounts: d.Accounts})
		river.AddWorker(cfg.Workers, &SweepWorker{accounts: d.Accounts})
		cfg.Queues[postgres.QueueAccount] = river.QueueConfig{MaxWorkers: _accountWorkers} //nolint:exhaustruct // defaults
		periodic(cfg, SweepArgs{})

		workers++
	}

	if d.Maintenance != nil {
		river.AddWorker(cfg.Workers, &MaintenanceWorker{maintenance: d.Maintenance})
		periodic(cfg, MaintenanceArgs{})

		workers++
	}

	return cfg, workers
}

// MailWorker sends the mails of the mail queue.
type MailWorker struct {
	river.WorkerDefaults[postgres.MailArgs]

	mail MailDeliverer
}

// Work implements river.Worker.
func (w *MailWorker) Work(ctx context.Context, job *river.Job[postgres.MailArgs]) error {
	return w.mail.DeliverMail(ctx, &repo.MailJob{Kind: job.Args.Mail, UserID: job.Args.UserID, Locale: job.Args.Locale}) //nolint:wrapcheck // retried by River
}

// PurgeWorker removes the data of deleted accounts.
type PurgeWorker struct {
	river.WorkerDefaults[postgres.PurgeArgs]

	accounts AccountPurger
}

// Work implements river.Worker.
func (w *PurgeWorker) Work(ctx context.Context, job *river.Job[postgres.PurgeArgs]) error {
	return w.accounts.Purge(ctx, job.Args.UserID) //nolint:wrapcheck // retried by River
}

// SweepArgs are the arguments of the hourly account.sweep job.
type SweepArgs struct{}

// Kind implements river.JobArgs.
func (SweepArgs) Kind() string { return "account.sweep" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (SweepArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: postgres.QueueAccount} //nolint:exhaustruct // defaults
}

// SweepWorker reschedules the purges of deleted accounts left unpurged.
type SweepWorker struct {
	river.WorkerDefaults[SweepArgs]

	accounts AccountPurger
}

// Work implements river.Worker.
func (w *SweepWorker) Work(ctx context.Context, _ *river.Job[SweepArgs]) error {
	return w.accounts.Sweep(ctx) //nolint:wrapcheck // retried by River
}

// MaintenanceArgs are the arguments of the hourly maintenance job.
type MaintenanceArgs struct{}

// Kind implements river.JobArgs.
func (MaintenanceArgs) Kind() string { return "maintenance" }

// MaintenanceWorker runs the cleanup.
type MaintenanceWorker struct {
	river.WorkerDefaults[MaintenanceArgs]

	maintenance Maintainer
}

// Work implements river.Worker.
func (w *MaintenanceWorker) Work(ctx context.Context, _ *river.Job[MaintenanceArgs]) error {
	return w.maintenance.Run(ctx) //nolint:wrapcheck // retried by River
}
