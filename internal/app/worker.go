package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
	"github.com/xingyunqi-tech/go-template/internal/usecase/alert"
	"github.com/xingyunqi-tech/go-template/internal/usecase/notification"
	"github.com/xingyunqi-tech/go-template/internal/worker"
)

const _stopAndCancelGrace = 5 * time.Second

// NewWorkerClient builds the River client that works the queues and schedules the periodic jobs.
func NewWorkerClient(cfg *config.Config, log *slog.Logger, infra *Infra, sender mail.Sender) (*river.Client[pgx.Tx], error) {
	translator, err := i18n.New(cfg.App.FallbackLanguage)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	composer, err := mail.NewComposer(translator, cfg.App.Name, cfg.App.FrontendDomain)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	if sender == nil {
		sender = newSender(&cfg.Mail, log)
	}

	appMetrics := infra.Metrics
	db := postgres.NewDB(infra.Pool, acquireTimeout(cfg), log)

	inserter, err := newInserter(infra.Pool, log)
	if err != nil {
		return nil, err
	}

	riverCfg, err := worker.Config(&worker.Deps{
		Composer: composer,
		Sender:   sender,
		Cleanup: newAccountCleanup(db, postgres.NewJobs(inserter), alert.New(postgres.NewAlerts(db), appMetrics, log),
			newFiles(cfg, db, log), log),
		Locks:       redis.NewLocks(infra.Redis, log),
		Maintenance: postgres.NewMaintenance(db),
		Counter:     appMetrics,
		Log:         log,
		Notify: worker.NotificationDeps{
			Notifications: notification.New(postgres.NewNotifications(db), postgres.NewJobs(inserter), log),
			Users:         postgres.NewUsers(db), SMS: nil, Subscribe: nil, Templates: nil,
		},
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // worker errors are wrapped
	}

	riverCfg.ErrorHandler = jobErrors{log: log, report: reportError(cfg)}

	client, err := river.NewClient(riverpgxv5.New(infra.Pool), riverCfg)
	if err != nil {
		return nil, fmt.Errorf("app: river: %w", err)
	}

	return client, nil
}

// Work runs the queue workers and periodic jobs until ctx is canceled, then lets running jobs
// finish within SHUTDOWN_TIMEOUT.
func Work(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	infra, err := OpenInfra(ctx, cfg, cfg.Database.WorkerMaxConnections)
	if err != nil {
		return err
	}
	defer infra.Close()

	client, err := NewWorkerClient(cfg, log, infra, nil)
	if err != nil {
		return err
	}

	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("app: start worker: %w", err)
	}

	log.InfoContext(ctx, "worker started")
	<-ctx.Done()
	log.InfoContext(ctx, "worker stopping", "timeout", cfg.App.ShutdownTimeout)

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.App.ShutdownTimeout)
	defer cancel()

	if err := client.Stop(stopCtx); err == nil || errors.Is(err, context.Canceled) {
		return nil
	}

	log.WarnContext(ctx, "worker stop timed out, canceling running jobs")

	cancelCtx, cancelStop := context.WithTimeout(context.WithoutCancel(ctx), _stopAndCancelGrace)
	defer cancelStop()

	if err := client.StopAndCancel(cancelCtx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("app: stop worker: %w", err)
	}

	return nil
}
