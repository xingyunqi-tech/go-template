package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

const _connectionStatsSQL = `SELECT
  numbackends AS active_connections,
  (SELECT setting::int FROM pg_settings WHERE name = 'max_connections') AS max_connections
FROM pg_stat_database
WHERE datname = current_database()`

// Health implements repo.DatabaseHealth.
type Health struct {
	db *DB
}

// NewHealth returns the database health probe.
func NewHealth(db *DB) *Health {
	return &Health{db: db}
}

var _ repo.DatabaseHealth = (*Health)(nil)

// Ping runs SELECT 1.
func (h *Health) Ping(ctx context.Context) error {
	return h.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		if _, err := db.Exec(ctx, "SELECT 1"); err != nil {
			return fmt.Errorf("postgres: ping: %w", err)
		}

		return nil
	})
}

// ConnectionStats returns the backends connected to the current database and max_connections.
func (h *Health) ConnectionStats(ctx context.Context) (active, maximum int, err error) {
	err = h.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		var a, m int32
		if scanErr := db.QueryRow(ctx, _connectionStatsSQL).Scan(&a, &m); scanErr != nil {
			return fmt.Errorf("postgres: connection stats: %w", scanErr)
		}

		active, maximum = int(a), int(m)

		return nil
	})

	return active, maximum, err
}
