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
	"github.com/xingyunqi-tech/go-template/internal/usecase/audit"
	maintenanceuc "github.com/xingyunqi-tech/go-template/internal/usecase/maintenance"
	"github.com/xingyunqi-tech/go-template/internal/worker"
)

const _stopAndCancelGrace = 5 * time.Second

// NewWorkerClient builds the River client that works the queues and schedules the periodic
// jobs; it is nil while no job is registered. The issuing service works the mail queue.
func NewWorkerClient(cfg *config.Config, log *slog.Logger, infra *Infra) (*river.Client[pgx.Tx], error) {
	db := postgres.NewDB(infra.Pool, _acquireTimeout, log)
	maintenance := &maintenanceuc.Deps{
		Identity: nil, Audits: audit.New(postgres.NewAudits(db)), Retention: cfg.Audit.Retention, Log: log, Now: nil,
	}
	deps := &worker.Deps{Schema: cfg.Database.Schema, Log: log, Mail: nil, Accounts: nil, Maintenance: nil}

	if cfg.Auth.Role() != config.RoleVerifier {
		files, err := newFiles(cfg, db, log)
		if err != nil {
			return nil, err
		}

		translator, err := i18n.New(cfg.App.DefaultLocale)
		if err != nil {
			return nil, fmt.Errorf("app: %w", err)
		}

		identity, err := newIdentity(&identityDeps{
			cfg: cfg, log: log, db: db, infra: infra, keys: nil,
			translator: translator, limiter: nil, revoker: redis.NewRevocations(infra.Redis), files: files,
		})
		if err != nil {
			return nil, err
		}

		deps.Mail, deps.Accounts = identity.Auth, identity.Accounts
		maintenance.Identity = postgres.NewMaintenance(db)
	}

	deps.Maintenance = maintenanceuc.New(maintenance)

	riverCfg, workers := worker.Config(deps)
	if workers == 0 {
		return nil, nil //nolint:nilnil // nothing to work
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
func Work(ctx context.Context, cfg *config.Config, log *slog.Logger, obs *Observability) error {
	infra, err := OpenInfra(ctx, cfg, cfg.Database.WorkerMaxConns)
	if err != nil {
		return err
	}
	defer infra.Close()

	client, err := NewWorkerClient(cfg, log, infra)
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

	if client == nil {
		log.InfoContext(ctx, "worker idle: no jobs registered")
		<-ctx.Done()

		return nil
	}

	if err := client.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("app: start worker: %w", err)
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
