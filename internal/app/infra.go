package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/multitracer"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/extra/redisotel/v9"
	goredis "github.com/redis/go-redis/v9"

	"github.com/xingyunqi-tech/go-template/internal/config"
	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	"github.com/xingyunqi-tech/go-template/internal/metrics"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	pgpool "github.com/xingyunqi-tech/go-template/pkg/postgres"
)

const _redisDialTimeout = time.Second

var errInvalidCA = errors.New("DATABASE_CA holds no PEM certificate")

// Infra holds the connections and collectors shared by the process.
type Infra struct {
	Pool    *pgxpool.Pool
	Redis   *goredis.Client
	Metrics *metrics.Metrics
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

// databaseTLS follows the node-postgres ssl options: DATABASE_CA, DATABASE_KEY and DATABASE_CERT
// are PEM text, and DATABASE_REJECT_UNAUTHORIZED=false skips verification.
func databaseTLS(db *config.Database) (*tls.Config, error) {
	if !db.SSLEnabled.Or(false) {
		return nil, nil //nolint:nilnil // TLS is off
	}

	host := db.Host
	if db.URL != "" {
		if u, err := url.Parse(db.URL); err == nil {
			host = u.Hostname()
		}
	}

	cfg := &tls.Config{ //nolint:exhaustruct // only the fields node-postgres sets
		MinVersion:         tls.VersionTLS12,
		ServerName:         host,
		InsecureSkipVerify: !db.RejectUnauthorized.Or(true), //nolint:gosec // opt-in via DATABASE_REJECT_UNAUTHORIZED=false
	}

	if db.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(db.CA)) {
			return nil, errInvalidCA
		}

		cfg.RootCAs = pool
	}

	if db.Cert != "" {
		cert, err := tls.X509KeyPair([]byte(db.Cert), []byte(db.Key.Value()))
		if err != nil {
			return nil, fmt.Errorf("DATABASE_CERT / DATABASE_KEY: %w", err)
		}

		cfg.Certificates = []tls.Certificate{cert}
	}

	return cfg, nil
}

// OpenInfra connects to PostgreSQL with maxConns connections and, when enabled, to Redis.
func OpenInfra(ctx context.Context, cfg *config.Config, maxConns int32) (*Infra, error) {
	tlsCfg, err := databaseTLS(&cfg.Database)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	appMetrics := metrics.New(cfg.App.Name, cfg.Metrics.Disabled.On())
	tracing := cfg.Observability.OTelEnabled.Or(false)

	var tracer pgx.QueryTracer = appMetrics.QueryTracer()
	if tracing {
		tracer = &multitracer.Tracer{QueryTracers: []pgx.QueryTracer{tracer, otelpgx.NewTracer()}} //nolint:exhaustruct // query tracing only
	}

	pool, err := pgpool.New(ctx, cfg.Database.DSN(), pgpool.Options{MaxConns: maxConns, TLS: tlsCfg, Tracer: tracer})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	infra := &Infra{Pool: pool, Redis: nil, Metrics: appMetrics}

	if cfg.Redis.Enabled.Or(false) {
		infra.Redis = goredis.NewClient(&goredis.Options{ //nolint:exhaustruct // defaults for the rest
			Addr:        cfg.Redis.Addr(),
			Password:    cfg.Redis.Password.Value(),
			DB:          cfg.Redis.DB,
			MaxRetries:  1,
			DialTimeout: _redisDialTimeout,
		})

		if tracing {
			if err := redisotel.InstrumentTracing(infra.Redis); err != nil {
				infra.Close()

				return nil, fmt.Errorf("app: redis tracing: %w", err)
			}
		}
	}

	return infra, nil
}

func acquireTimeout(cfg *config.Config) time.Duration {
	return time.Duration(cfg.Database.ConnectionTimeoutMS) * time.Millisecond
}

// transactor adapts postgres.DB to the controller's Transactor.
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
