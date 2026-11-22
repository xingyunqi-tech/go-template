// Package postgres implements the repo ports with sqlc queries over pgx.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// DB hands out the request transaction when there is one, else a pooled connection acquired
// within the acquire timeout.
type DB struct {
	pool           *pgxpool.Pool
	acquireTimeout time.Duration
	log            *slog.Logger
}

// NewDB wraps pool.
func NewDB(pool *pgxpool.Pool, acquireTimeout time.Duration, log *slog.Logger) *DB {
	return &DB{pool: pool, acquireTimeout: acquireTimeout, log: log}
}

type txKey struct{}

// txState is a request transaction; it takes a connection and begins at its first statement.
type txState struct {
	db          *DB
	conn        *pgxpool.Conn
	tx          pgx.Tx
	afterCommit []func(context.Context)
}

// open begins the transaction unless it already has.
func (s *txState) open(ctx context.Context) (pgx.Tx, error) {
	if s.tx != nil {
		return s.tx, nil
	}

	conn, err := s.db.acquire(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()

		return nil, fmt.Errorf("postgres: begin: %w", err)
	}

	s.conn, s.tx = conn, tx

	return tx, nil
}

// WithoutTx returns ctx detached from the request transaction.
func WithoutTx(ctx context.Context) context.Context {
	return context.WithValue(ctx, txKey{}, (*txState)(nil))
}

func stateOf(ctx context.Context) *txState {
	s, _ := ctx.Value(txKey{}).(*txState)

	return s
}

// dbtx runs fn with the request transaction, or with a connection held only for fn.
func (d *DB) dbtx(ctx context.Context, fn func(sqlcgen.DBTX) error) error {
	if s := stateOf(ctx); s != nil {
		tx, err := s.open(ctx)
		if err != nil {
			return err
		}

		return fn(tx)
	}

	conn, err := d.acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	return fn(conn)
}

func (d *DB) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, d.acquireTimeout)
	defer cancel()

	conn, err := d.pool.Acquire(acquireCtx)
	if err != nil {
		return nil, entity.Wrap(entity.KeyServiceUnavailable, fmt.Errorf("postgres: acquire connection: %w", err))
	}

	return conn, nil
}

func (d *DB) queries(ctx context.Context, fn func(*sqlcgen.Queries) error) error {
	return d.dbtx(ctx, func(db sqlcgen.DBTX) error { return fn(sqlcgen.New(db)) })
}

// Tx is a request transaction.
type Tx struct {
	db    *DB
	state *txState
}

// Begin returns a context carrying a transaction that takes its connection and begins at its
// first statement.
func (d *DB) Begin(ctx context.Context) (context.Context, *Tx, error) {
	state := &txState{db: d, conn: nil, tx: nil, afterCommit: nil}

	return context.WithValue(ctx, txKey{}, state), &Tx{db: d, state: state}, nil
}

// Commit commits and then runs the after-commit callbacks; committed is false when PostgreSQL
// rolled the failed transaction back instead.
func (t *Tx) Commit(ctx context.Context) (committed bool, err error) {
	if t.state.tx != nil {
		err = t.state.tx.Commit(ctx)
		t.state.conn.Release()
	}

	if err != nil {
		if errors.Is(err, pgx.ErrTxCommitRollback) {
			return false, nil
		}

		return false, fmt.Errorf("postgres: commit: %w", err)
	}

	callbacks := t.state.afterCommit
	t.state.afterCommit = nil
	after := context.WithValue(context.WithoutCancel(ctx), txKey{}, (*txState)(nil))

	for _, fn := range callbacks {
		fn(after)
	}

	return true, nil
}

// Rollback discards the transaction and its after-commit callbacks.
func (t *Tx) Rollback(ctx context.Context) {
	t.state.afterCommit = nil

	if t.state.tx == nil {
		return
	}

	defer t.state.conn.Release()

	if err := t.state.tx.Rollback(context.WithoutCancel(ctx)); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.db.log.WarnContext(ctx, "rollback failed", "error", err.Error())
	}
}

// AfterCommit runs fn after the request transaction commits, or right away without one.
func (d *DB) AfterCommit(ctx context.Context, fn func(context.Context)) {
	if s := stateOf(ctx); s != nil {
		s.afterCommit = append(s.afterCommit, fn)

		return
	}

	fn(ctx)
}

// IsDatabaseError reports an error raised by a PostgreSQL statement.
func IsDatabaseError(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr)
}

// ErrRolledBack reports a transaction PostgreSQL rolled back at commit after a failed statement.
var ErrRolledBack = errors.New("postgres: transaction rolled back")

// InTx runs fn in a transaction of its own, committed when fn succeeds.
func (d *DB) InTx(ctx context.Context, fn func(ctx context.Context) error) error {
	txCtx, tx, err := d.Begin(WithoutTx(ctx))
	if err != nil {
		return err
	}

	if err := fn(txCtx); err != nil {
		tx.Rollback(ctx)

		return err
	}

	committed, err := tx.Commit(ctx)
	if err != nil {
		return err
	}

	if !committed {
		return ErrRolledBack
	}

	return nil
}
