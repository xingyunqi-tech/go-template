//go:build integration

package ratelimit_test

import (
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

func client(t *testing.T) *goredis.Client {
	t.Helper()

	db := _redis.Borrow(t)
	c := goredis.NewClient(&goredis.Options{Addr: db.Addr, DB: db.DB}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func TestRedisStoresTheTATInMilliseconds(t *testing.T) {
	t.Parallel()

	c := client(t)
	l := ratelimit.New(c)
	rule := ratelimit.Rule{Name: "auth-login", Rate: 20, Period: time.Minute, Burst: 10}

	before := time.Now().UnixMilli()

	d, err := l.Consume(t.Context(), rule, "ip:203.0.113.7")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, 9, d.Remaining)
	assert.Equal(t, int64(3), d.ResetSeconds())

	stored, err := c.Get(t.Context(), "rl:auth-login:ip:203.0.113.7").Result()
	require.NoError(t, err)

	tat, err := strconv.ParseInt(stored, 10, 64)
	require.NoError(t, err)
	assert.InDelta(t, before+3000, tat, 1000)

	ttl, err := c.PTTL(t.Context(), "rl:auth-login:ip:203.0.113.7").Result()
	require.NoError(t, err)
	assert.InDelta(t, 3000, ttl.Milliseconds(), 1000)
}

func TestRedisBurstPeekAndReset(t *testing.T) {
	t.Parallel()

	c := client(t)
	l := ratelimit.New(c)
	rule := ratelimit.Rule{Name: "auth-login-account", Rate: 10, Period: time.Hour, Burst: 5}

	for range 5 {
		d, err := l.Consume(t.Context(), rule, "a:0123456789abcdef")
		require.NoError(t, err)
		assert.True(t, d.Allowed)
	}

	d, err := l.Peek(t.Context(), rule, "a:0123456789abcdef")
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Equal(t, int64(360), d.RetryAfterSeconds())

	require.NoError(t, l.Reset(t.Context(), rule, "a:0123456789abcdef"))

	d, err = l.Peek(t.Context(), rule, "a:0123456789abcdef")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, 5, d.Remaining)

	exists, err := c.Exists(t.Context(), "rl:auth-login-account:a:0123456789abcdef").Result()
	require.NoError(t, err)
	assert.Zero(t, exists)
}

func TestRedisFailureIsReturned(t *testing.T) {
	t.Parallel()

	c := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = c.Close() })

	_, err := ratelimit.New(c).Consume(t.Context(), ratelimit.Rule{Name: "x", Rate: 1, Period: time.Second, Burst: 1}, "ip:1")
	require.Error(t, err)
}
