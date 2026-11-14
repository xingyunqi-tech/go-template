//go:build integration

package ratelimit_test

import (
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/testutil"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

func TestRedisScriptFollowsGcra(t *testing.T) {
	t.Parallel()

	r, err := testutil.StartRedis(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Terminate() })

	client := goredis.NewClient(&goredis.Options{Addr: r.Addr}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	o := &observer{}
	l := ratelimit.New(client, o)

	first := l.Consume(t.Context(), "throttle:a:default:gcra", time.Minute, 1)
	assert.True(t, first.Allowed)
	assert.Equal(t, int64(60), first.ResetSeconds())

	second := l.Consume(t.Context(), "throttle:a:default:gcra", time.Minute, 1)
	assert.False(t, second.Allowed)
	assert.Equal(t, int64(60), second.RetryAfterSeconds())

	stored, err := client.Get(t.Context(), "throttle:a:default:gcra").Result()
	require.NoError(t, err)

	tat, err := ratelimit.ParseTAT(stored)
	require.NoError(t, err)
	assert.InDelta(t, time.Now().Add(time.Minute).UnixMicro(), tat, float64(5*time.Second.Microseconds()))

	ttl, err := client.PTTL(t.Context(), "throttle:a:default:gcra").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 55*time.Second)

	future := float64(time.Now().Add(30*time.Second).UnixMicro()) / 1000
	require.NoError(t, client.Set(t.Context(), "throttle:b:default:gcra", strconv.FormatFloat(future, 'f', 8, 64), time.Minute).Err())

	blocked := l.Consume(t.Context(), "throttle:b:default:gcra", time.Minute, 1)
	assert.False(t, blocked.Allowed)
	assert.InDelta(t, 30, blocked.RetryAfterSeconds(), 2)

	allowed := l.Consume(t.Context(), "throttle:b:default:gcra", time.Minute, 3)
	assert.True(t, allowed.Allowed)
	assert.Zero(t, o.fallbacks)
}
