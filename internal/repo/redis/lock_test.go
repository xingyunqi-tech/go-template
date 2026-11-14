package redis_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
)

func TestLocalLockIsExclusiveUntilReleasedOrExpired(t *testing.T) {
	t.Parallel()

	l := redis.NewLocks(nil, slog.New(slog.DiscardHandler))

	token, ok := l.TryAcquire(t.Context(), "job", time.Hour)
	assert.True(t, ok)

	_, ok = l.TryAcquire(t.Context(), "job", time.Hour)
	assert.False(t, ok)

	l.Release(t.Context(), "job", "someone-else")

	_, ok = l.TryAcquire(t.Context(), "job", time.Hour)
	assert.False(t, ok)

	l.Release(t.Context(), "job", token)

	_, ok = l.TryAcquire(t.Context(), "job", 10*time.Millisecond)
	assert.True(t, ok)

	time.Sleep(20 * time.Millisecond)

	_, ok = l.TryAcquire(t.Context(), "job", time.Hour)
	assert.True(t, ok)
}
