package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Sessions implements repo.Sessions.
type Sessions struct {
	db *DB
}

// NewSessions returns Sessions.
func NewSessions(db *DB) *Sessions {
	return &Sessions{db: db}
}

var _ repo.Sessions = (*Sessions)(nil)

func sessionOf(s *sqlcgen.Session) *repo.SessionRecord {
	return &repo.SessionRecord{
		Session: entity.Session{
			ID: s.ID, UserID: s.UserID, AuthTime: s.AuthTime, ExpiresAt: s.ExpiresAt, UserAgent: s.UserAgent, IP: s.Ip,
			CreatedAt: s.CreatedAt, LastActiveAt: s.LastActiveAt,
		},
		RevokedAt: s.RevokedAt,
	}
}

// Create implements repo.Sessions.
func (r *Sessions) Create(ctx context.Context, s *repo.NewSession) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		_, err := q.CreateSession(ctx, sqlcgen.CreateSessionParams{
			ID: s.ID, UserID: s.UserID, RefreshHash: s.RefreshHash, AuthTime: s.AuthTime, ExpiresAt: s.ExpiresAt,
			UserAgent: s.UserAgent, Ip: s.IP,
		})

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("postgres: create session: %w", err)
	}

	return nil
}

func (r *Sessions) one(ctx context.Context, fn func(q *sqlcgen.Queries) (*sqlcgen.Session, error)) (*repo.SessionRecord, bool, error) {
	var out *repo.SessionRecord

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := fn(q)
		if err != nil {
			return err
		}

		out = sessionOf(row)

		return nil
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("postgres: lock session: %w", err)
	}

	return out, true, nil
}

// ByRefreshForUpdate implements repo.Sessions.
func (r *Sessions) ByRefreshForUpdate(ctx context.Context, hash []byte) (*repo.SessionRecord, bool, error) {
	return r.one(ctx, func(q *sqlcgen.Queries) (*sqlcgen.Session, error) { return q.GetSessionByRefreshForUpdate(ctx, hash) })
}

// ForUpdate implements repo.Sessions.
func (r *Sessions) ForUpdate(ctx context.Context, id uuid.UUID) (*repo.SessionRecord, bool, error) {
	return r.one(ctx, func(q *sqlcgen.Queries) (*sqlcgen.Session, error) { return q.GetSessionForUpdate(ctx, id) })
}

// History implements repo.Sessions.
func (r *Sessions) History(ctx context.Context, hash []byte) (*repo.RefreshHistory, bool, error) {
	var out *repo.RefreshHistory

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetRefreshHistory(ctx, hash)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		out = &repo.RefreshHistory{SessionID: row.SessionID, RotatedAt: row.RotatedAt}

		return nil
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("postgres: refresh history: %w", err)
	}

	return out, true, nil
}

// Rotate implements repo.Sessions.
func (r *Sessions) Rotate(ctx context.Context, id uuid.UUID, oldHash, newHash []byte, now time.Time) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		if err := q.InsertRefreshHistory(ctx, sqlcgen.InsertRefreshHistoryParams{RefreshHash: oldHash, SessionID: id, RotatedAt: now}); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		return q.RotateSessionRefresh(ctx, sqlcgen.RotateSessionRefreshParams{ID: id, RefreshHash: newHash, LastActiveAt: now}) //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("postgres: rotate session: %w", err)
	}

	return nil
}

// Revoke implements repo.Sessions.
func (r *Sessions) Revoke(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.RevokeSession(ctx, sqlcgen.RevokeSessionParams{ID: id, UserID: userID})

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return false, fmt.Errorf("postgres: revoke session: %w", err)
	}

	return n > 0, nil
}

// RevokeAll implements repo.Sessions.
func (r *Sessions) RevokeAll(ctx context.Context, userID uuid.UUID, keep *uuid.UUID) ([]uuid.UUID, error) {
	var ids []uuid.UUID

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		ids, err = q.RevokeUserSessions(ctx, sqlcgen.RevokeUserSessionsParams{UserID: userID, KeepID: keep})

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: revoke sessions: %w", err)
	}

	return ids, nil
}

// ListActive implements repo.Sessions.
func (r *Sessions) ListActive(ctx context.Context, userID uuid.UUID) ([]entity.Session, error) {
	var out []entity.Session

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListActiveSessions(ctx, userID)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		out = make([]entity.Session, 0, len(rows))
		for _, row := range rows {
			out = append(out, sessionOf(row).Session)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list sessions: %w", err)
	}

	return out, nil
}
