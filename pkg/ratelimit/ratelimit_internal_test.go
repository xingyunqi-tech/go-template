package ratelimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _rule = Rule{Name: "svc-ip", Rate: 60, Period: time.Minute, Burst: 3} //nolint:gochecknoglobals // fixture

func memoryLimiter(start time.Time) (*Limiter, *time.Time) {
	l := New(nil)
	now := start
	l.now = func() time.Time { return now }

	return l, &now
}

func TestBurstPassesBackToBackThenRecoversAtTheRate(t *testing.T) {
	t.Parallel()

	l, now := memoryLimiter(time.UnixMilli(1_000_000))

	for i, want := range []int{2, 1, 0} {
		d, err := l.Consume(t.Context(), _rule, "ip:1.1.1.1")
		require.NoError(t, err)
		assert.True(t, d.Allowed, i)
		assert.Equal(t, 3, d.Limit)
		assert.Equal(t, want, d.Remaining, i)
		assert.Equal(t, int64(i+1), d.ResetSeconds(), i)
	}

	d, err := l.Consume(t.Context(), _rule, "ip:1.1.1.1")
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Equal(t, 0, d.Remaining)
	assert.Equal(t, int64(1), d.RetryAfterSeconds())
	assert.Equal(t, int64(3), d.ResetSeconds())

	*now = now.Add(time.Second)

	d, err = l.Consume(t.Context(), _rule, "ip:1.1.1.1")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, 0, d.Remaining)

	other, err := l.Consume(t.Context(), _rule, "ip:2.2.2.2")
	require.NoError(t, err)
	assert.Equal(t, 2, other.Remaining)
}

func TestPeekDoesNotCount(t *testing.T) {
	t.Parallel()

	l, _ := memoryLimiter(time.UnixMilli(5_000_000))

	for range 3 {
		d, err := l.Peek(t.Context(), _rule, "a:x")
		require.NoError(t, err)
		assert.True(t, d.Allowed)
		assert.Equal(t, 3, d.Remaining)
	}

	for range 3 {
		_, err := l.Consume(t.Context(), _rule, "a:x")
		require.NoError(t, err)
	}

	d, err := l.Peek(t.Context(), _rule, "a:x")
	require.NoError(t, err)
	assert.False(t, d.Allowed)

	require.NoError(t, l.Reset(t.Context(), _rule, "a:x"))

	d, err = l.Peek(t.Context(), _rule, "a:x")
	require.NoError(t, err)
	assert.True(t, d.Allowed)
	assert.Equal(t, 3, d.Remaining)
}

func TestGcraWithASlowRate(t *testing.T) {
	t.Parallel()

	rule := Rule{Name: "auth-register", Rate: 10, Period: time.Hour, Burst: 3}

	d, next := Gcra(rule, 0, 1000, false)
	assert.True(t, d.Allowed)
	assert.Equal(t, 2, d.Remaining)
	assert.Equal(t, int64(1000+360_000), next)
	assert.Equal(t, int64(360), d.ResetSeconds())
}

func TestTightestPicksFewestRemaining(t *testing.T) {
	t.Parallel()

	a := Decision{Allowed: true, Limit: 100, Remaining: 50, ResetMillis: 1000, RetryMillis: 0}
	b := Decision{Allowed: true, Limit: 5, Remaining: 2, ResetMillis: 9000, RetryMillis: 0}

	got, ok := Tightest(a, b)
	assert.True(t, ok)
	assert.Equal(t, b, got)

	_, ok = Tightest()
	assert.False(t, ok)
}

func TestMemoryEvictsWhenFull(t *testing.T) {
	t.Parallel()

	l, _ := memoryLimiter(time.UnixMilli(1_000))

	for i := range _memoryMaxKeys + 5 {
		_, err := l.Consume(t.Context(), _rule, time.Duration(i).String())
		require.NoError(t, err)
	}

	assert.LessOrEqual(t, len(l.tat), _memoryMaxKeys)
}
