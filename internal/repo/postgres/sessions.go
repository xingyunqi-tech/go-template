package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Sessions implements repo.Sessions.
type Sessions struct {
	db *DB
}

// NewSessions returns the sessions repository.
func NewSessions(db *DB) *Sessions {
	return &Sessions{db: db}
}

var _ repo.Sessions = (*Sessions)(nil)

func toSession(s *sqlcgen.Session) *entity.Session {
	return &entity.Session{
		ID: int(s.ID), Hash: s.Hash, UserID: intPtr(s.UserId), UserAgent: s.UserAgent, IP: s.Ip,
		CreatedAt: s.CreatedAt, LastActiveAt: s.LastActiveAt,
	}
}

func id32(id int) int32 {
	return int32(id) //nolint:gosec // ids are int4
}

// Get returns the active session, or nil.
func (r *Sessions) Get(ctx context.Context, id int) (*entity.Session, error) {
	var out *entity.Session

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		s, err := q.GetSession(ctx, id32(id))
		if notFound(err) {
			return nil
		}

		if err == nil {
			out = toSession(s)
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("sessions: get: %w", err)
	}

	return out, nil
}

// Create inserts a session with lastActiveAt = now.
func (r *Sessions) Create(ctx context.Context, userID int, hash string, userAgent, ip *string) (*entity.Session, error) {
	var out *entity.Session

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		uid := id32(userID)

		s, err := q.CreateSession(ctx, sqlcgen.CreateSessionParams{UserID: &uid, Hash: hash, UserAgent: userAgent, Ip: ip})
		if err == nil {
			out = toSession(s)
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("sessions: create: %w", err)
	}

	return out, nil
}

// Rotate replaces current with next when the session still holds current.
func (r *Sessions) Rotate(ctx context.Context, id int, current, next string) (bool, error) {
	var rows int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		rows, err = q.RotateSessionHash(ctx, sqlcgen.RotateSessionHashParams{Next: next, ID: id32(id), Current: current})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("sessions: rotate: %w", err)
	}

	return rows > 0, nil
}

// ListActive returns the user's sessions, most recently active first.
func (r *Sessions) ListActive(ctx context.Context, userID int) ([]entity.Session, error) {
	var out []entity.Session

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		uid := id32(userID)

		rows, err := q.ListActiveSessions(ctx, &uid)
		for _, s := range rows {
			out = append(out, *toSession(s))
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("sessions: list: %w", err)
	}

	return out, nil
}

// Authenticate loads the session with its user and throttles the lastActiveAt update on the same
// connection; a failed update is only logged.
func (r *Sessions) Authenticate(ctx context.Context, id int) (*repo.SessionAuth, error) {
	var out *repo.SessionAuth

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.AuthenticateSession(ctx, id32(id))
		if notFound(err) {
			return nil
		}

		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		out = &repo.SessionAuth{
			SessionID: int(row.SessionID), UserID: int(row.UserID),
			StatusID: deref(intPtr(row.StatusID)), RoleID: intPtr(row.RoleID),
		}

		if err := q.TouchSession(ctx, row.SessionID); err != nil {
			r.db.log.WarnContext(ctx, "update session lastActiveAt failed", "session_id", id, "error", err.Error())
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("sessions: authenticate: %w", err)
	}

	return out, nil
}

// DeleteForUser soft-deletes the session when it belongs to userID.
func (r *Sessions) DeleteForUser(ctx context.Context, id, userID int) (bool, error) {
	var rows int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		uid := id32(userID)

		var err error

		rows, err = q.SoftDeleteUserSession(ctx, sqlcgen.SoftDeleteUserSessionParams{ID: id32(id), UserID: &uid})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("sessions: delete for user: %w", err)
	}

	return rows > 0, nil
}

// Delete soft-deletes the session.
func (r *Sessions) Delete(ctx context.Context, id int) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		return q.SoftDeleteSession(ctx, id32(id))
	})
	if err != nil {
		return fmt.Errorf("sessions: delete: %w", err)
	}

	return nil
}

// DeleteAllForUser soft-deletes the user's sessions except one and returns the deleted ids.
func (r *Sessions) DeleteAllForUser(ctx context.Context, userID int, except *int) ([]int, error) {
	var ids []int

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		uid := id32(userID)

		rows, err := q.SoftDeleteUserSessions(ctx, sqlcgen.SoftDeleteUserSessionsParams{UserID: &uid, ExceptID: int32Ptr(except)})
		for _, id := range rows {
			ids = append(ids, int(id))
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("sessions: delete all: %w", err)
	}

	return ids, nil
}
