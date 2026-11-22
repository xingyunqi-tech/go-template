//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/migrations"
	pgpool "github.com/xingyunqi-tech/go-template/pkg/postgres"
)

func loadConfig(t *testing.T, dsn string) *config.Config {
	t.Helper()

	cfg, err := config.Load()
	require.NoError(t, err)

	cfg.Database.URL = config.Secret(dsn)

	return cfg
}

type testApp struct {
	t     *testing.T
	url   string
	cfg   *config.Config
	infra *app.Infra
}

func newApp(t *testing.T, mutate func(*config.Config), metrics http.Handler) *testApp {
	t.Helper()

	cfg := loadConfig(t, _db.Clone(t))
	if mutate != nil {
		mutate(cfg)
	}

	infra, err := app.OpenInfra(t.Context(), cfg, 0)
	require.NoError(t, err)
	t.Cleanup(infra.Close)

	handler, err := app.NewHandler(t.Context(), cfg, slog.New(slog.DiscardHandler), infra, metrics)
	require.NoError(t, err)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &testApp{t: t, url: srv.URL, cfg: cfg, infra: infra}
}

type result struct {
	Status int
	Header http.Header
	Raw    string
	Body   map[string]any
}

func (a *testApp) get(path, token string) *result {
	a.t.Helper()

	req, err := http.NewRequestWithContext(a.t.Context(), http.MethodGet, a.url+path, http.NoBody)
	require.NoError(a.t, err)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(a.t, err)

	out := &result{Status: res.StatusCode, Header: res.Header, Raw: string(raw), Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)

	return out
}

func freePort(t *testing.T) int {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.NoError(t, ln.Close())

	return addr.Port
}

// waitReady polls /health/ready of base until it answers 200.
func waitReady(t *testing.T, base string) {
	t.Helper()

	require.Eventually(t, func() bool {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/health/ready", http.NoBody)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()

		return res.StatusCode == http.StatusOK
	}, 10*time.Second, 20*time.Millisecond)
}

func TestServeUntilCanceled(t *testing.T) {
	t.Parallel()

	cfg := loadConfig(t, _db.Clone(t))
	cfg.App.Port = freePort(t)

	obs, err := app.SetupObservability(t.Context(), cfg, "test")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx, cfg, slog.New(slog.DiscardHandler), obs) }()

	waitReady(t, "http://127.0.0.1:"+strconv.Itoa(cfg.App.Port))

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
	}
}

func TestServeFailsWhenPortTaken(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)

	cfg := loadConfig(t, _db.Clone(t))
	cfg.App.Port = addr.Port

	obs, err := app.SetupObservability(t.Context(), cfg, "test")
	require.NoError(t, err)

	require.Error(t, app.Serve(t.Context(), cfg, slog.New(slog.DiscardHandler), obs))
}

var _constraintName = regexp.MustCompile(`^[a-z0-9_]+_(pkey|key|fkey|idx|check)$`)

func tablesIn(t *testing.T, pool *pgxpool.Pool, schema string) []string {
	t.Helper()

	rows, err := pool.Query(t.Context(), `SELECT tablename FROM pg_tables WHERE schemaname = $1 ORDER BY tablename`, schema)
	require.NoError(t, err)

	var tables []string

	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		tables = append(tables, name)
	}

	require.NoError(t, rows.Err())

	return tables
}

func TestMigrationsLiveInTheServiceSchema(t *testing.T) {
	t.Parallel()

	dsn := _db.Clone(t)
	log := slog.New(slog.DiscardHandler)

	pool, err := pgpool.New(t.Context(), dsn, pgpool.Options{MaxConns: 2, Schema: "svc_test", Tracer: nil})
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	sets := []migrations.Set{migrations.Core()}

	require.NoError(t, app.MigrateUp(t.Context(), pool, "svc_test", sets, log))
	require.NoError(t, app.MigrateUp(t.Context(), pool, "svc_test", sets, log))

	tables := tablesIn(t, pool, "svc_test")
	assert.Contains(t, tables, "audit_log")
	assert.Contains(t, tables, "goose_core_version")
	assert.Contains(t, tables, "river_job")

	rows, err := pool.Query(t.Context(), `
SELECT con.conname FROM pg_constraint con JOIN pg_namespace n ON n.oid = con.connamespace
JOIN pg_class c ON c.oid = con.conrelid
WHERE n.nspname = 'svc_test' AND con.contype <> 'n' AND c.relname NOT LIKE 'river%' AND c.relname NOT LIKE 'goose%'
UNION ALL
SELECT indexname FROM pg_indexes WHERE schemaname = 'svc_test' AND tablename NOT LIKE 'river%' AND tablename NOT LIKE 'goose%'`)
	require.NoError(t, err)

	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		assert.Regexp(t, _constraintName, name)
	}

	require.NoError(t, rows.Err())

	require.NoError(t, app.MigrateDown(t.Context(), pool, "svc_test", sets, log))
	assert.Equal(t, []string{"goose_core_version"}, tablesIn(t, pool, "svc_test"))

	require.NoError(t, app.MigrateUp(t.Context(), pool, "svc_test", sets, log))
	assert.Contains(t, tablesIn(t, pool, "svc_test"), "audit_log")
}
