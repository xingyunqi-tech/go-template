package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
)

var errRolledBack = errors.New("transaction rolled back by the database")

// Tx is an open request transaction.
type Tx interface {
	Commit(ctx context.Context) (committed bool, err error)
	Rollback(ctx context.Context)
}

// Transactor opens request transactions.
type Transactor interface {
	Begin(ctx context.Context) (context.Context, Tx, error)
}

// commitsOnError reports whether the writes before err are kept: 4xx business and HTTP errors
// commit, server errors and errors raised by SQL statements roll back.
func commitsOnError(err error) bool {
	status := httperr.StatusCode(err)

	return status >= http.StatusBadRequest && status < http.StatusInternalServerError && !httperr.IsDatabaseError(err)
}

// transactional runs the handlers of Tx policies in a transaction committed before the response
// is written.
func transactional(db Transactor, policies map[string]Policy) gen.StrictMiddlewareFunc {
	return func(f gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
		if !policies[operationID].Tx || db == nil {
			return f
		}

		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			return runInTx(ctx, db, func(txCtx context.Context) (any, error) {
				return f(txCtx, w, r.WithContext(txCtx), request)
			})
		}
	}
}

func runInTx(ctx context.Context, db Transactor, f func(context.Context) (any, error)) (resp any, err error) {
	txCtx, tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback(ctx)
			panic(p)
		}
	}()

	resp, err = f(txCtx)
	if err != nil && !commitsOnError(err) {
		tx.Rollback(ctx)

		return resp, err
	}

	committed, commitErr := tx.Commit(context.WithoutCancel(txCtx))

	switch {
	case commitErr != nil:
		return nil, commitErr //nolint:wrapcheck // repo errors are wrapped
	case !committed && err == nil:
		return nil, errRolledBack
	default:
		return resp, err
	}
}
