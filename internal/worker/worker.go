// Package worker holds the River workers and periodic jobs of the worker process.
package worker

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
)

const _mailWorkers = 4

// MailDeliverer runs a mail job.
type MailDeliverer interface {
	DeliverMail(ctx context.Context, job *repo.MailJob) error
}

// Deps are what the workers use; a nil Mail registers no mail worker.
type Deps struct {
	Schema string
	Log    *slog.Logger
	Mail   MailDeliverer
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
