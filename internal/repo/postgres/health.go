package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Health pings PostgreSQL.
type Health struct {
	db *DB
}

// NewHealth returns the database health probe.
func NewHealth(db *DB) *Health {
	return &Health{db: db}
}

var _ repo.Pinger = (*Health)(nil)

// Ping runs SELECT 1.
func (h *Health) Ping(ctx context.Context) error {
	return h.db.dbtx(WithoutTx(ctx), func(db sqlcgen.DBTX) error {
		if _, err := db.Exec(ctx, "SELECT 1"); err != nil {
			return fmt.Errorf("postgres: ping: %w", err)
		}

		return nil
	})
}
