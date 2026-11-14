package http

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

type fakeTx struct {
	committed, rolledBack bool
	commitResult          bool
	commitErr             error
}

func (f *fakeTx) Commit(context.Context) (bool, error) {
	f.committed = true

	return f.commitResult, f.commitErr
}

func (f *fakeTx) Rollback(context.Context) {
	f.rolledBack = true
}

type fakeTransactor struct {
	tx  *fakeTx
	err error
}

func (f fakeTransactor) Begin(ctx context.Context) (context.Context, Tx, error) {
	return ctx, f.tx, f.err
}

func TestTransactionEndRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		commit   bool
		rollback bool
	}{
		{"success commits", nil, true, false},
		{"business error commits", entity.Unauthorized(""), true, false},
		{"validation error commits", entity.Invalid("email", "notFound"), true, false},
		{"database error rolls back", &pgconn.PgError{Code: "23505", ConstraintName: "user_phone_key"}, false, true},
		{"server error rolls back", errors.New("boom"), false, true},
	}

	for _, c := range cases {
		tx := &fakeTx{commitResult: true}

		_, err := runInTx(t.Context(), fakeTransactor{tx: tx, err: nil}, func(context.Context) (any, error) { return "ok", c.err })
		assert.ErrorIs(t, err, c.err, c.name)
		assert.Equal(t, c.commit, tx.committed, c.name)
		assert.Equal(t, c.rollback, tx.rolledBack, c.name)
	}
}

func TestTransactionFailures(t *testing.T) {
	t.Parallel()

	begin := errors.New("no connection")
	_, err := runInTx(t.Context(), fakeTransactor{tx: nil, err: begin}, func(context.Context) (any, error) { return nil, nil })
	require.ErrorIs(t, err, begin)

	commit := errors.New("commit failed")
	_, err = runInTx(t.Context(), fakeTransactor{tx: &fakeTx{commitErr: commit}, err: nil}, func(context.Context) (any, error) { return "ok", nil })
	require.ErrorIs(t, err, commit)

	_, err = runInTx(t.Context(), fakeTransactor{tx: &fakeTx{commitResult: false}, err: nil}, func(context.Context) (any, error) { return "ok", nil })
	require.ErrorIs(t, err, errRolledBack)

	tx := &fakeTx{}

	assert.Panics(t, func() {
		_, _ = runInTx(t.Context(), fakeTransactor{tx: tx, err: nil}, func(context.Context) (any, error) { panic("boom") })
	})
	assert.True(t, tx.rolledBack)
}
