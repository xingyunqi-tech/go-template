package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

func TestInProcessIdempotencyDropsExpiredKeys(t *testing.T) {
	t.Parallel()

	s := NewIdempotency(nil)
	now := time.Now()
	s.now = func() time.Time { return now }

	_, err := s.Begin(t.Context(), "k1", "h", "o1", time.Minute)
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)

	existing, err := s.Begin(t.Context(), "k2", "h", "o2", time.Minute)
	require.NoError(t, err)
	assert.Nil(t, existing)
	assert.Len(t, s.local, 1)

	existing, err = s.Begin(t.Context(), "k1", "h", "o3", time.Minute)
	require.NoError(t, err)
	assert.Nil(t, existing)
}

func TestInProcessIdempotencyPurgesAtMostOncePerMinute(t *testing.T) {
	t.Parallel()

	s := NewIdempotency(nil)
	now := time.Now()
	s.now = func() time.Time { return now }

	_, err := s.Begin(t.Context(), "k1", "h", "o1", time.Second)
	require.NoError(t, err)

	now = now.Add(2 * time.Second)

	_, err = s.Begin(t.Context(), "k2", "h", "o2", time.Second)
	require.NoError(t, err)
	assert.Len(t, s.local, 2)

	now = now.Add(time.Minute)

	_, err = s.Begin(t.Context(), "k3", "h", "o3", time.Second)
	require.NoError(t, err)
	assert.Len(t, s.local, 1)
}

func TestInProcessIdempotencyHonoursOnlyTheClaimOwner(t *testing.T) {
	t.Parallel()

	s := NewIdempotency(nil)
	now := time.Now()
	s.now = func() time.Time { return now }
	done := &repo.IdempotencyRecord{Hash: "h", Owner: "", Done: true, Status: 201, ContentType: "", Body: nil}

	_, err := s.Begin(t.Context(), "k", "h", "first", time.Minute)
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)

	_, err = s.Begin(t.Context(), "k", "h", "second", time.Minute)
	require.NoError(t, err)

	require.ErrorIs(t, s.Complete(t.Context(), "k", "first", done, time.Hour), repo.ErrIdempotencyClaimLost)
	require.NoError(t, s.Release(t.Context(), "k", "first"))

	existing, err := s.Begin(t.Context(), "k", "h", "third", time.Minute)
	require.NoError(t, err)
	require.NotNil(t, existing)
	assert.Equal(t, "second", existing.Owner)

	require.NoError(t, s.Complete(t.Context(), "k", "second", done, time.Hour))
	require.NoError(t, s.Release(t.Context(), "k", "second"))

	existing, err = s.Begin(t.Context(), "k", "h", "third", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, done, existing)
}

// vanishingKey answers every SET NX as taken and every GET as missing.
type vanishingKey struct{}

func (vanishingKey) DialHook(next goredis.DialHook) goredis.DialHook { return next }

func (vanishingKey) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return next
}

func (vanishingKey) ProcessHook(goredis.ProcessHook) goredis.ProcessHook {
	return func(_ context.Context, cmd goredis.Cmder) error {
		switch c := cmd.(type) {
		case *goredis.BoolCmd:
			c.SetVal(false)
		case *goredis.StringCmd:
			c.SetErr(goredis.Nil)

			return goredis.Nil
		default:
			return errors.New("unexpected command " + cmd.Name())
		}

		return nil
	}
}

func TestClaimGivesUpWhenTheKeyKeepsVanishing(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	client.AddHook(vanishingKey{})

	t.Cleanup(func() { _ = client.Close() })

	_, err := NewIdempotency(client).Begin(t.Context(), "k", "h", "o", time.Minute)
	require.ErrorIs(t, err, errClaimContended)
}
