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

func TestLocksAreSharedThroughRedis(t *testing.T) {
	t.Parallel()

	db := _redis.Borrow(t)
	client := goredis.NewClient(&goredis.Options{Addr: db.Addr, DB: db.DB}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	first := redis.NewLocks(client, slog.New(slog.DiscardHandler))
	second := redis.NewLocks(client, slog.New(slog.DiscardHandler))

	token, ok := first.TryAcquire(t.Context(), "account-cleanup:user-1", time.Minute)
	require.True(t, ok)

	stored, err := client.Get(t.Context(), "lock:account-cleanup:user-1").Result()
	require.NoError(t, err)
	assert.Equal(t, token, stored)

	ttl, err := client.PTTL(t.Context(), "lock:account-cleanup:user-1").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 50*time.Second)

	_, ok = second.TryAcquire(t.Context(), "account-cleanup:user-1", time.Minute)
	assert.False(t, ok)

	second.Release(t.Context(), "account-cleanup:user-1", "not-the-holder")
	assert.Equal(t, int64(1), client.Exists(t.Context(), "lock:account-cleanup:user-1").Val())

	first.Release(t.Context(), "account-cleanup:user-1", token)
	assert.Equal(t, int64(0), client.Exists(t.Context(), "lock:account-cleanup:user-1").Val())

	_, ok = second.TryAcquire(t.Context(), "account-cleanup:user-1", time.Minute)
	assert.True(t, ok)
}

func TestLocksFallBackToProcessWhenRedisFails(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	l := redis.NewLocks(client, slog.New(slog.DiscardHandler))

	token, ok := l.TryAcquire(t.Context(), "k", time.Minute)
	require.True(t, ok)

	_, ok = l.TryAcquire(t.Context(), "k", time.Minute)
	assert.False(t, ok)

	l.Release(t.Context(), "k", token)

	_, ok = l.TryAcquire(t.Context(), "k", time.Minute)
	assert.True(t, ok)
}
