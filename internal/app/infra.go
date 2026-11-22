package app

import (
	"context"
	"fmt"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/extra/redisotel/v9"
	goredis "github.com/redis/go-redis/v9"

	"github.com/xingyunqi-tech/go-template/internal/config"
	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	pgpool "github.com/xingyunqi-tech/go-template/pkg/postgres"
)

const _acquireTimeout = 5 * time.Second

// Infra holds the connections shared by the process.
type Infra struct {
	Pool  *pgxpool.Pool
	Redis *goredis.Client
}

// Close releases the connections.
func (i *Infra) Close() {
	if i.Redis != nil {
		_ = i.Redis.Close()
	}

	if i.Pool != nil {
		i.Pool.Close()
	}
}

// OpenInfra connects to PostgreSQL with maxConns connections in DATABASE_SCHEMA and, when
// REDIS_URL is set, to Redis; both are instrumented with OpenTelemetry.
func OpenInfra(ctx context.Context, cfg *config.Config, maxConns int32) (*Infra, error) {
	pool, err := pgpool.New(ctx, cfg.Database.DSN(), pgpool.Options{
		MaxConns: maxConns, Schema: cfg.Database.Schema, Tracer: otelpgx.NewTracer(),
	})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	infra := &Infra{Pool: pool, Redis: nil}

	if err := otelpgx.RecordStats(pool); err != nil {
		infra.Close()

		return nil, fmt.Errorf("app: pool metrics: %w", err)
	}

	if cfg.Redis.Enabled() {
		opts, err := goredis.ParseURL(cfg.Redis.URL.Value())
		if err != nil {
			infra.Close()

			return nil, fmt.Errorf("app: REDIS_URL: %w", err)
		}

		infra.Redis = goredis.NewClient(opts)

		if err := redisotel.InstrumentTracing(infra.Redis); err != nil {
			infra.Close()

			return nil, fmt.Errorf("app: redis tracing: %w", err)
		}

		if err := redisotel.InstrumentMetrics(infra.Redis); err != nil {
			infra.Close()

			return nil, fmt.Errorf("app: redis metrics: %w", err)
		}
	}

	return infra, nil
}

// transactor adapts postgres.DB to the router's Transactor.
type transactor struct {
	db *postgres.DB
}

func (t transactor) Begin(ctx context.Context) (context.Context, httpapi.Tx, error) {
	ctx, tx, err := t.db.Begin(ctx)
	if err != nil {
		return ctx, nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return ctx, tx, nil
}
