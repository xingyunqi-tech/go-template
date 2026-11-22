//go:build integration

package redis_test

import (
	"log/slog"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
)

func newClient(t *testing.T) *goredis.Client {
	t.Helper()

	db := _redis.Borrow(t)
	client := goredis.NewClient(&goredis.Options{Addr: db.Addr, DB: db.DB}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func TestCacheStoresJSONUnderTheServicePrefix(t *testing.T) {
	t.Parallel()

	client := newClient(t)
	c := redis.NewCache(client, "svc", slog.New(slog.DiscardHandler))
	require.NoError(t, c.Set(t.Context(), "profile:1", map[string]any{"name": "<a>", "n": 1}, time.Minute))

	raw, err := client.Get(t.Context(), "svc:cache:profile:1").Result()
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"<a>","n":1}`, raw)
	assert.Contains(t, raw, "<a>")

	ttl, err := client.PTTL(t.Context(), "svc:cache:profile:1").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 50*time.Second)

	var got map[string]any

	found, err := c.Get(t.Context(), "profile:1", &got)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, map[string]any{"name": "<a>", "n": 1.0}, got)

	require.NoError(t, client.Set(t.Context(), "svc:cache:broken", "{", time.Minute).Err())

	found, err = c.Get(t.Context(), "broken", &got)
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, c.Delete(t.Context(), "profile:1"))
	assert.Zero(t, client.Exists(t.Context(), "svc:cache:profile:1").Val())
}

func TestCacheFallsBackToMemoryWhenRedisIsDown(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	c := redis.NewCache(client, "svc", slog.New(slog.DiscardHandler))
	require.NoError(t, c.Set(t.Context(), "k", "v", time.Minute))

	var got string

	found, err := c.Get(t.Context(), "k", &got)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "v", got)
	require.NoError(t, c.Delete(t.Context(), "k"))
}
