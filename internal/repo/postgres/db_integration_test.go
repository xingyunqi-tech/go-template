//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

const _insertAudit = `INSERT INTO audit_log (action, resource_type) VALUES ($1, 'test')`

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

func countAudits(t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()

	var n int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_log WHERE action = $1`, action).Scan(&n))

	return n
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
		inside = postgres.Exec(ctx, db, _insertAudit, "after")
	})

	require.NoError(t, postgres.Exec(ctx, db, _insertAudit, "inside"))
	assert.False(t, committed)

	ok, err := tx.Commit(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, committed)
	require.NoError(t, inside)
	assert.Equal(t, 1, countAudits(t, pool, "inside"))
	assert.Equal(t, 1, countAudits(t, pool, "after"))

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

	require.NoError(t, postgres.Exec(ctx, db, _insertAudit, "kept?"))

	err = postgres.Exec(ctx, db, _insertAudit, nil)
	require.Error(t, err)
	assert.True(t, postgres.IsDatabaseError(err))

	ok, err := tx.Commit(ctx)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, countAudits(t, pool, "kept?"))
}

func TestInTxRollsBackOnError(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)
	boom := errors.New("boom")

	var inside error

	err := db.InTx(t.Context(), func(ctx context.Context) error {
		inside = postgres.Exec(ctx, db, _insertAudit, "rolled back")

		return boom
	})
	require.NoError(t, inside)
	require.ErrorIs(t, err, boom)
	assert.Zero(t, countAudits(t, pool, "rolled back"))
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
	err = postgres.NewHealth(db).Ping(t.Context())

	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestAuditsWriteTheAuditLog(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)
	actor := uuid.NewV7()

	require.NoError(t, postgres.NewAudits(db).Create(t.Context(), &repo.AuditLog{
		ActorSub: &actor, Action: "note.create", ResourceType: "note", ResourceID: "r1", RequestID: "req-1", IP: "203.0.113.7",
		Data: map[string]any{"operationId": "createNote"},
	}))

	var (
		gotActor uuid.UUID
		data     map[string]any
	)

	require.NoError(t, pool.QueryRow(t.Context(), `SELECT actor_sub, data FROM audit_log WHERE request_id = 'req-1'`).Scan(&gotActor, &data))
	assert.Equal(t, actor, gotActor)
	assert.Equal(t, map[string]any{"operationId": "createNote"}, data)
}

func TestInTxReportsATransactionTheDatabaseRolledBack(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)

	err := db.InTx(t.Context(), func(ctx context.Context) error {
		require.NoError(t, postgres.Exec(ctx, db, _insertAudit, "swallowed"))
		_ = postgres.Exec(ctx, db, _insertAudit, nil)

		return nil
	})
	require.ErrorIs(t, err, postgres.ErrRolledBack)
	assert.Zero(t, countAudits(t, pool, "swallowed"))
}

func TestRequestTransactionTakesAConnectionAtItsFirstStatement(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)

	ctx, tx, err := db.Begin(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int32(0), pool.Stat().AcquiredConns())

	require.NoError(t, postgres.Exec(ctx, db, _insertAudit, "lazy"))
	assert.Equal(t, int32(1), pool.Stat().AcquiredConns())

	ok, err := tx.Commit(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int32(0), pool.Stat().AcquiredConns())
	assert.Equal(t, 1, countAudits(t, pool, "lazy"))

	_, empty, err := db.Begin(t.Context())
	require.NoError(t, err)

	ok, err = empty.Commit(t.Context())
	require.NoError(t, err)
	assert.True(t, ok)
}
