//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

var _db *testutil.Database //nolint:gochecknoglobals // one container per package

func TestMain(m *testing.M) {
	db, err := testutil.StartDatabase(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}

	_db = db
	code := m.Run()

	db.Close()
	os.Exit(code)
}

func newDB(t *testing.T) (*postgres.DB, *pgxpool.Pool) {
	t.Helper()

	pool, err := pgxpool.New(t.Context(), _db.Clone(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return postgres.NewDB(pool, time.Second, slog.New(slog.DiscardHandler)), pool
}

func TestAfterCommitRunsOnlyAfterCommit(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)

	ran := false

	db.AfterCommit(t.Context(), func(context.Context) { ran = true })
	assert.True(t, ran)

	ctx, tx, err := db.Begin(t.Context())
	require.NoError(t, err)

	var inside error

	committed := false

	db.AfterCommit(ctx, func(ctx context.Context) {
		committed = true
		_, inside = postgres.NewUsers(db).Get(ctx, repo.UserQuery{Email: ptr("john.doe@example.com")})
	})

	_, err = postgres.NewSessions(db).DeleteAllForUser(ctx, 2, nil)
	require.NoError(t, err)
	assert.False(t, committed)

	ok, err := tx.Commit(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, committed)
	require.NoError(t, inside)

	ctx, tx, err = db.Begin(t.Context())
	require.NoError(t, err)

	rolledBack := false

	db.AfterCommit(ctx, func(context.Context) { rolledBack = true })
	tx.Rollback(ctx)
	assert.False(t, rolledBack)
	assert.Equal(t, int32(0), pool.Stat().AcquiredConns())
}

func TestCommitOfFailedTransactionReportsRollback(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)

	ctx, tx, err := db.Begin(t.Context())
	require.NoError(t, err)

	users := postgres.NewUsers(db)
	_, err = users.Create(ctx, &repo.NewUser{FirstName: ptr("kept?"), Provider: "email"}) //nolint:exhaustruct // other columns default
	require.NoError(t, err)

	_, err = users.Create(ctx, &repo.NewUser{Email: ptr("john.doe@example.com"), Provider: "email"}) //nolint:exhaustruct // other columns default
	require.Error(t, err)
	assert.True(t, postgres.IsDatabaseError(err))

	ok, err := tx.Commit(ctx)
	require.NoError(t, err)
	assert.False(t, ok)

	var n int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM "user" WHERE "firstName" = 'kept?'`).Scan(&n))
	assert.Zero(t, n)
}

func TestAcquireTimesOut(t *testing.T) {
	t.Parallel()

	cfg, err := pgxpool.ParseConfig(_db.Clone(t))
	require.NoError(t, err)

	cfg.MaxConns = 1

	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	held, err := pool.Acquire(t.Context())
	require.NoError(t, err)

	defer held.Release()

	db := postgres.NewDB(pool, 100*time.Millisecond, slog.New(slog.DiscardHandler))

	start := time.Now()
	_, err = postgres.NewUsers(db).Get(t.Context(), repo.UserQuery{ID: ptr(1)})
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestListOrdersAndRejectsUnknownProperties(t *testing.T) {
	t.Parallel()

	db, _ := newDB(t)
	users := postgres.NewUsers(db)

	list, err := users.List(t.Context(), &repo.UserList{Sort: []repo.SortField{{Property: "email", Descending: true}}, Offset: 0, Limit: 10, RoleIDs: nil})
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "john.doe@example.com", *list[0].Email)

	_, err = users.List(t.Context(), &repo.UserList{Sort: []repo.SortField{{Property: "nope", Descending: false}}, Offset: 0, Limit: 10, RoleIDs: nil})

	var unknown *postgres.ErrUnknownSortProperty
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, `Property "nope" was not found in "UserEntity"`, err.Error())
}

func ptr[T any](v T) *T {
	return &v
}
