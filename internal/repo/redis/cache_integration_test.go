//go:build integration

package redis_test

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
)

func TestCacheUsesKeyvFormatInRedis(t *testing.T) {
	t.Parallel()

	db := _redis.Borrow(t)
	client := goredis.NewClient(&goredis.Options{Addr: db.Addr, DB: db.DB}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	c := redis.NewCache(client, slog.New(slog.DiscardHandler))
	require.NoError(t, c.Set(t.Context(), "jwt:session:1", map[string]any{"statusId": 1, "roleId": nil}, time.Minute))

	raw, err := client.Get(t.Context(), "cache::jwt:session:1").Bytes()
	require.NoError(t, err)

	var stored struct {
		Value   map[string]any `json:"value"`
		Expires int64          `json:"expires"`
	}
	require.NoError(t, json.Unmarshal(raw, &stored))
	assert.Equal(t, map[string]any{"statusId": 1.0, "roleId": nil}, stored.Value)
	assert.InDelta(t, time.Now().Add(time.Minute).UnixMilli(), stored.Expires, 5000)

	ttl, err := client.PTTL(t.Context(), "cache::jwt:session:1").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 50*time.Second)

	var got map[string]any

	found, err := c.Get(t.Context(), "jwt:session:1", &got)
	require.NoError(t, err)
	assert.True(t, found)

	require.NoError(t, client.Set(t.Context(), "cache::nestjs", `{"value":"from nestjs","expires":4102444800000}`, time.Minute).Err())

	var s string

	found, err = c.Get(t.Context(), "nestjs", &s)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "from nestjs", s)

	require.NoError(t, client.Set(t.Context(), "cache::old", `{"value":1,"expires":1}`, time.Minute).Err())

	var n int

	found, err = c.Get(t.Context(), "old", &n)
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, c.Delete(t.Context(), "jwt:session:1"))
	assert.Zero(t, client.Exists(t.Context(), "cache::jwt:session:1").Val())
}

func TestCacheFallsBackToMemoryWhenRedisIsDown(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	c := redis.NewCache(client, slog.New(slog.DiscardHandler))
	require.NoError(t, c.Set(t.Context(), "k", "v", time.Minute))

	var got string

	found, err := c.Get(t.Context(), "k", &got)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "v", got)
	require.NoError(t, c.Delete(t.Context(), "k"))
}
