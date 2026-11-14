package ratelimit_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

const (
	_minute = int64(60_000_000)
	_fixed  = "268382706.34399077"
)

func fixedNow(t *testing.T) int64 {
	t.Helper()

	now, err := ratelimit.ParseTAT(_fixed)
	require.NoError(t, err)

	return now
}

func TestFreshKeyWithLimitOneIsAllowed(t *testing.T) {
	t.Parallel()

	now := fixedNow(t)
	assert.Equal(t, int64(268_382_706_344), now)

	d, next := ratelimit.Gcra(0, now, _minute, 1)
	assert.True(t, d.Allowed)
	assert.Equal(t, int64(0), d.Remaining)
	assert.Equal(t, int64(60), d.ResetSeconds())
	assert.Equal(t, now+_minute, next)

	d, _ = ratelimit.Gcra(next, now, _minute, 1)
	assert.False(t, d.Allowed)
	assert.Equal(t, int64(60), d.RetryAfterSeconds())
}

func TestRemainingCountsDown(t *testing.T) {
	t.Parallel()

	m := ratelimit.NewMemory()
	now := fixedNow(t)

	for want := int64(4); want >= 0; want-- {
		d := m.Consume("k", _minute, 5, now)
		require.True(t, d.Allowed)
		assert.Equal(t, want, d.Remaining)
	}

	d := m.Consume("k", _minute, 5, now)
	assert.False(t, d.Allowed)
	assert.Equal(t, int64(12), d.RetryAfterSeconds())

	assert.True(t, m.Consume("k", _minute, 5, now+_minute/5).Allowed)
}

func TestTATIsReadFromMillisecondStrings(t *testing.T) {
	t.Parallel()

	tat, err := ratelimit.ParseTAT(_fixed)
	require.NoError(t, err)

	d, next := ratelimit.Gcra(tat, tat-1000, _minute, 1)
	assert.False(t, d.Allowed)
	assert.Equal(t, int64(1), d.RetryAfterSeconds())
	assert.Equal(t, "268382706.344", ratelimit.FormatTAT(tat))
	assert.Equal(t, tat, next)

	_, err = ratelimit.ParseTAT("x")
	require.Error(t, err)
}

func TestMemoryIsBounded(t *testing.T) {
	t.Parallel()

	m := ratelimit.NewMemory()
	for i := range 10_005 {
		m.Consume(strconv.Itoa(i), _minute, 1, int64(i))
	}

	assert.True(t, m.Consume("0", _minute, 1, 10_006).Allowed)
}

type observer struct {
	fallbacks, firsts, recovered int
}

func (o *observer) Fallback(_ context.Context, _ error, first bool) {
	o.fallbacks++
	if first {
		o.firsts++
	}
}

func (o *observer) Recovered(context.Context) {
	o.recovered++
}

func TestLimiterFallsBackToMemoryWhenRedisFails(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 100 * time.Millisecond}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	o := &observer{}
	l := ratelimit.New(client, o)

	assert.True(t, l.Consume(t.Context(), "k", time.Minute, 1).Allowed)
	assert.False(t, l.Consume(t.Context(), "k", time.Minute, 1).Allowed)
	assert.Equal(t, 2, o.fallbacks)
	assert.Equal(t, 1, o.firsts)

	memoryOnly := ratelimit.New(nil, nil)
	assert.True(t, memoryOnly.Consume(t.Context(), "k", time.Minute, 1).Allowed)
}
