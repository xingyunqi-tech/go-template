package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// _maxRows bounds the row limits passed as int32.
const _maxRows = 1 << 20

// Maintenance implements repo.Maintenance.
type Maintenance struct {
	db *DB
}

// NewMaintenance returns Maintenance.
func NewMaintenance(db *DB) *Maintenance {
	return &Maintenance{db: db}
}

var _ repo.Maintenance = (*Maintenance)(nil)

func (m *Maintenance) count(ctx context.Context, op string, fn func(q *sqlcgen.Queries) (int64, error)) (int64, error) {
	var n int64

	err := m.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = fn(q)

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("postgres: %s: %w", op, err)
	}

	return n, nil
}

// DeleteExpiredSessions implements repo.Maintenance; their refresh history goes with them.
func (m *Maintenance) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	return m.count(ctx, "delete expired sessions", func(q *sqlcgen.Queries) (int64, error) { return q.DeleteExpiredSessions(ctx, now) })
}

// DeleteSpentTokens implements repo.Maintenance: expired, used and invalidated tokens.
func (m *Maintenance) DeleteSpentTokens(ctx context.Context, now time.Time) (int64, error) {
	return m.count(ctx, "delete spent tokens", func(q *sqlcgen.Queries) (int64, error) { return q.DeleteSpentTokens(ctx, now) })
}

// UnpurgedUsers implements repo.Maintenance.
func (m *Maintenance) UnpurgedUsers(ctx context.Context, before time.Time, after *repo.UnpurgedUser, limit int) ([]repo.UnpurgedUser, error) {
	params := sqlcgen.ListUnpurgedUsersParams{Before: &before, AfterDeletedAt: nil, AfterID: nil, RowLimit: int32(min(limit, _maxRows))} //nolint:gosec // bounded
	if after != nil {
		params.AfterDeletedAt, params.AfterID = &after.DeletedAt, &after.ID
	}

	var out []repo.UnpurgedUser

	err := m.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListUnpurgedUsers(ctx, params)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		out = make([]repo.UnpurgedUser, 0, len(rows))
		for _, row := range rows {
			out = append(out, repo.UnpurgedUser{ID: row.ID, DeletedAt: *row.DeletedAt})
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: unpurged users: %w", err)
	}

	return out, nil
}
