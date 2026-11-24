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
	"github.com/xingyunqi-tech/go-template/internal/events"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	accountuc "github.com/xingyunqi-tech/go-template/internal/usecase/account"
	"github.com/xingyunqi-tech/go-template/internal/usecase/audit"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	maintenanceuc "github.com/xingyunqi-tech/go-template/internal/usecase/maintenance"
	"github.com/xingyunqi-tech/go-template/internal/worker"
)

const (
	_stopAndCancelGrace = 5 * time.Second
	_subscribeRetry     = time.Second
	_subscribeRetryMax  = time.Minute
)

// subscribe consumes user.deleted until ctx is canceled, subscribing again with a growing delay
// after a failure.
func subscribe(ctx context.Context, sub events.Subscriber, h events.Handler, log *slog.Logger) {
	delay := _subscribeRetry

	for {
		err := sub.Subscribe(ctx, events.TypeUserDeleted, h)
		if err == nil || ctx.Err() != nil {
			return
		}

		log.ErrorContext(ctx, "user.deleted subscription failed, retrying", "error", err.Error(), "retry_in", delay)

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		delay = min(delay*2, _subscribeRetryMax) //nolint:mnd // doubling
	}
}

// NewWorkerClient builds the River client that works the queues and schedules the periodic jobs.
// The issuing service works the mail queue.
func NewWorkerClient(cfg *config.Config, log *slog.Logger, infra *Infra) (*river.Client[pgx.Tx], error) {
	client, _, err := newWorker(cfg, log, infra)

	return client, err
}

// newWorker also returns the handler of the user.deleted events a verifier consumes.
func newWorker(cfg *config.Config, log *slog.Logger, infra *Infra) (*river.Client[pgx.Tx], events.Handler, error) {
	db := postgres.NewDB(infra.Pool, _acquireTimeout, log)
	maintenance := &maintenanceuc.Deps{
		Identity: nil, Audits: audit.New(postgres.NewAudits(db)), Retention: cfg.Audit.Retention, Log: log, Now: nil,
	}
	deps := &worker.Deps{Schema: cfg.Database.Schema, Log: log, Mail: nil, Accounts: nil, Maintenance: nil}

	files, err := newFiles(cfg, db, log)
	if err != nil {
		return nil, nil, err
	}

	var onUserDeleted events.Handler

	if cfg.Auth.Role() == config.RoleVerifier {
		accounts, err := verifierAccounts(cfg, log, infra, db, files)
		if err != nil {
			return nil, nil, err
		}

		deps.Accounts, onUserDeleted = accounts, accounts.OnUserDeleted
	} else {
		identity, err := workerIdentity(cfg, log, infra, db, files)
		if err != nil {
			return nil, nil, err
		}

		deps.Mail, deps.Accounts = identity.Auth, identity.Accounts
		maintenance.Identity = postgres.NewMaintenance(db)
	}

	deps.Maintenance = maintenanceuc.New(maintenance)

	riverCfg, _ := worker.Config(deps)
	riverCfg.ErrorHandler = jobErrors{log: log, report: reportError(cfg)}

	client, err := river.NewClient(riverpgxv5.New(infra.Pool), riverCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("app: river: %w", err)
	}

	return client, onUserDeleted, nil
}

// workerIdentity builds the identity use cases the worker of the issuing service runs.
func workerIdentity(cfg *config.Config, log *slog.Logger, infra *Infra, db *postgres.DB, files *file.Service) (*Identity, error) {
	translator, err := i18n.New(cfg.App.DefaultLocale)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	return newIdentity(&identityDeps{
		cfg: cfg, log: log, db: db, infra: infra, keys: nil,
		translator: translator, limiter: nil, revoker: redis.NewRevocations(infra.Redis), files: files,
	})
}

// verifierAccounts purges the data of this service for the users the issuer deleted.
func verifierAccounts(cfg *config.Config, log *slog.Logger, infra *Infra, db *postgres.DB, files *file.Service) (*accountuc.Service, error) {
	inserter, err := newInsertClient(cfg, log, infra)
	if err != nil {
		return nil, err
	}

	return accountuc.New(&accountuc.Deps{
		Users: nil, Identities: nil, Revocation: nil, Queue: postgres.NewAccountQueue(inserter), Unpurged: nil,
		Purgers:   corePurgers(db, files),
		Publisher: nil, Tx: db, Log: log, Grace: 0, Source: cfg.Telemetry.ServiceName, Now: nil,
	}), nil
}

// Work runs the queue workers and periodic jobs until ctx is canceled, then lets running jobs
// finish within SHUTDOWN_TIMEOUT.
func Work(ctx context.Context, cfg *config.Config, log *slog.Logger, obs *Observability) error {
	infra, err := OpenInfra(ctx, cfg, cfg.Database.WorkerMaxConns)
	if err != nil {
		return err
	}
	defer infra.Close()

	client, onUserDeleted, err := newWorker(cfg, log, infra)
	if err != nil {
		return err
	}

	if metrics := obs.MetricsHandler(); metrics != nil && cfg.Metrics.WorkerPort != 0 {
		stop, err := serveMetrics(ctx, cfg, cfg.Metrics.WorkerPort, metrics, log)
		if err != nil {
			return err
		}
		defer stop()
	}

	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("app: start worker: %w", err)
	}

	if onUserDeleted != nil {
		go subscribe(ctx, events.NewLog(log), onUserDeleted, log)
	}

	log.InfoContext(ctx, "worker started")
	<-ctx.Done()
	log.InfoContext(ctx, "worker stopping", "timeout", cfg.App.ShutdownTimeout)

	return stopWorker(ctx, client, cfg.App.ShutdownTimeout, log)
}

// stopWorker lets running jobs finish within timeout, then cancels them.
func stopWorker(ctx context.Context, client *river.Client[pgx.Tx], timeout time.Duration, log *slog.Logger) error {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
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
