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

type txState struct {
	tx          pgx.Tx
	afterCommit []func(context.Context)
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
		return fn(s.tx)
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
		return nil, fmt.Errorf("postgres: acquire connection: %w", err)
	}

	return conn, nil
}

func (d *DB) queries(ctx context.Context, fn func(*sqlcgen.Queries) error) error {
	return d.dbtx(ctx, func(db sqlcgen.DBTX) error { return fn(sqlcgen.New(db)) })
}

// Tx is an open request transaction.
type Tx struct {
	db    *DB
	conn  *pgxpool.Conn
	state *txState
}

// Begin opens a transaction on its own connection and returns a context carrying it.
func (d *DB) Begin(ctx context.Context) (context.Context, *Tx, error) {
	conn, err := d.acquire(ctx)
	if err != nil {
		return ctx, nil, err
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		conn.Release()

		return ctx, nil, fmt.Errorf("postgres: begin: %w", err)
	}

	state := &txState{tx: tx, afterCommit: nil}

	return context.WithValue(ctx, txKey{}, state), &Tx{db: d, conn: conn, state: state}, nil
}

// Commit commits and then runs the after-commit callbacks; committed is false when PostgreSQL
// rolled the failed transaction back instead.
func (t *Tx) Commit(ctx context.Context) (committed bool, err error) {
	err = t.state.tx.Commit(ctx)
	t.conn.Release()

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
	defer t.conn.Release()

	t.state.afterCommit = nil

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

	if _, err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}
