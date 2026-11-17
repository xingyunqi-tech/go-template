//go:build integration

package app_test

import (
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
	"github.com/xingyunqi-tech/go-template/internal/usecase/permission"
)

func TestCreateSuperuserCreatesAnActiveAdministrator(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, nil, nil)
	log := slog.New(slog.DiscardHandler)

	require.NoError(t, app.CreateSuperuser("root@example.com", "Rootpass01")(t.Context(), a.cfg, log))

	root := a.login("root@example.com", md5Hex("Rootpass01"))

	me := a.do(http.MethodGet, "/api/v1/auth/me", root.access, nil)
	role, _ := me.data()["role"].(map[string]any)
	assert.InDelta(t, 1, role["id"], 0)

	err := app.CreateSuperuser("root@example.com", "Rootpass01")(t.Context(), a.cfg, log)
	require.ErrorIs(t, err, postgres.ErrUserExists)
}

func TestClearPermissionCacheWithoutRedisSucceeds(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, nil, nil)

	require.NoError(t, app.ClearPermissionCache(t.Context(), a.cfg, slog.New(slog.DiscardHandler)))
}

func withRedis(t *testing.T, cfg *config.Config, addr string) *config.Config {
	t.Helper()

	host, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	out := *cfg
	require.NoError(t, out.Redis.Enabled.UnmarshalText([]byte("true")))
	out.Redis.Host = host
	out.Redis.Port, err = strconv.Atoi(port)
	require.NoError(t, err)

	return &out
}

func TestClearPermissionCacheDeletesTheRedisKey(t *testing.T) {
	t.Parallel()

	r, err := testutil.StartRedis(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Terminate() })

	client := goredis.NewClient(&goredis.Options{Addr: r.Addr}) //nolint:exhaustruct // defaults for the rest

	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Set(t.Context(), permission.CacheKey, `{"2":["user:read"]}`, 0).Err())

	a := newAppWith(t, 0, nil, nil)
	require.NoError(t, app.ClearPermissionCache(t.Context(), withRedis(t, a.cfg, r.Addr), slog.New(slog.DiscardHandler)))

	exists, err := client.Exists(t.Context(), permission.CacheKey).Result()
	require.NoError(t, err)
	assert.Zero(t, exists)
}

func TestClearPermissionCacheFailsWhenRedisIsUnreachable(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, nil, nil)

	require.Error(t, app.ClearPermissionCache(t.Context(), withRedis(t, a.cfg, "127.0.0.1:1"), slog.New(slog.DiscardHandler)))
}
