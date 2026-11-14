// Package postgres opens a pgx connection pool pinned to the UTC session time zone.
package postgres

import (
	"context"
	"crypto/tls"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Options tunes the pool; zero values keep the pgx defaults.
type Options struct {
	MaxConns int32
	TLS      *tls.Config
}

// New parses dsn, pins every connection to timezone UTC and verifies connectivity.
func New(ctx context.Context, dsn string, opts Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}

	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"

	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}

	if opts.TLS != nil {
		cfg.ConnConfig.TLSConfig = opts.TLS
		cfg.ConnConfig.Fallbacks = nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return pool, nil
}
