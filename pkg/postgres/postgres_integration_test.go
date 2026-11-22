//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/testutil"
	"github.com/xingyunqi-tech/go-template/pkg/postgres"
)

var dsn string //nolint:gochecknoglobals // set once in TestMain

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	pg, err := testutil.StartPostgres(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	defer func() { _ = pg.Terminate() }()

	dsn = pg.DSN

	return m.Run()
}

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := postgres.New(t.Context(), dsn, postgres.Options{MaxConns: 2, Schema: "", Tracer: nil})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}

func TestSessionTimeZoneIsUTC(t *testing.T) {
	t.Parallel()

	var tz string
	require.NoError(t, newPool(t).QueryRow(t.Context(), "SHOW timezone").Scan(&tz))
	assert.Equal(t, "UTC", tz)
}

func TestTimestampRoundTripsAsUTC(t *testing.T) {
	t.Parallel()

	var got time.Time

	err := newPool(t).QueryRow(t.Context(), "SELECT '2026-11-10 12:00:00'::timestamp").Scan(&got)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), got)
}

func TestStdlibUUIDCodec(t *testing.T) {
	t.Parallel()

	pool := newPool(t)
	ctx := t.Context()
	id := uuid.NewV7()

	var text string
	require.NoError(t, pool.QueryRow(ctx, "SELECT $1::uuid::text", id).Scan(&text))
	assert.Equal(t, id.String(), text)

	var back uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, "SELECT $1::uuid", id).Scan(&back))
	assert.Equal(t, id, back)

	var generated uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, "SELECT gen_random_uuid()").Scan(&generated))
	assert.NotEqual(t, uuid.Nil(), generated)

	var null *uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, "SELECT NULL::uuid").Scan(&null))
	assert.Nil(t, null)

	var ids []uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, "SELECT ARRAY[$1::uuid, $2::uuid]", id, generated).Scan(&ids))
	assert.Equal(t, []uuid.UUID{id, generated}, ids)
}

func TestNewRejectsUnreachableServer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	_, err := postgres.New(ctx, "postgres://app:app@127.0.0.1:1/app?sslmode=disable", postgres.Options{MaxConns: 0, Schema: "", Tracer: nil})
	require.Error(t, err)
}

func TestSchemaBecomesTheSearchPath(t *testing.T) {
	t.Parallel()

	pool, err := postgres.New(t.Context(), dsn, postgres.Options{MaxConns: 1, Schema: "svc_path", Tracer: nil})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var path string
	require.NoError(t, pool.QueryRow(t.Context(), "SHOW search_path").Scan(&path))
	require.Equal(t, `"svc_path"`, path)
}
