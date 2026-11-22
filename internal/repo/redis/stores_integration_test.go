//go:build integration

package redis_test

import (
	"testing"
	"time"
	"uuid"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
)

func TestRevocationsUseTheSharedKey(t *testing.T) {
	t.Parallel()

	client := newClient(t)
	r := redis.NewRevocations(client)
	sid := uuid.NewV7()

	revoked, err := r.IsRevoked(t.Context(), sid)
	require.NoError(t, err)
	assert.False(t, revoked)

	require.NoError(t, r.Revoke(t.Context(), sid, 16*time.Minute))

	revoked, err = r.IsRevoked(t.Context(), sid)
	require.NoError(t, err)
	assert.True(t, revoked)

	ttl, err := client.PTTL(t.Context(), "auth:revoked:sid:"+sid.String()).Result()
	require.NoError(t, err)
	assert.InDelta(t, (16 * time.Minute).Seconds(), ttl.Seconds(), 5)
}

func TestRevocationsReportRedisFailures(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	_, err := redis.NewRevocations(client).IsRevoked(t.Context(), uuid.NewV7())
	require.Error(t, err)
}

func TestIdempotencyClaimCompleteRelease(t *testing.T) {
	t.Parallel()

	for name, client := range map[string]*goredis.Client{"redis": newClient(t), "memory": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := redis.NewIdempotency(client)

			existing, err := s.Begin(t.Context(), "svc:idem:k1", "h1", "o1", time.Hour)
			require.NoError(t, err)
			assert.Nil(t, existing)

			existing, err = s.Begin(t.Context(), "svc:idem:k1", "h1", "o2", time.Hour)
			require.NoError(t, err)
			require.NotNil(t, existing)
			assert.False(t, existing.Done)
			assert.Equal(t, "o1", existing.Owner)

			require.NoError(t, s.Release(t.Context(), "svc:idem:k1", "o2"))

			done := &repo.IdempotencyRecord{Hash: "h1", Done: true, Status: 201, ContentType: "application/json", Body: []byte(`{"a":1}`)}
			require.ErrorIs(t, s.Complete(t.Context(), "svc:idem:k1", "o2", done, time.Hour), repo.ErrIdempotencyClaimLost)
			require.NoError(t, s.Complete(t.Context(), "svc:idem:k1", "o1", done, time.Hour))

			require.NoError(t, s.Release(t.Context(), "svc:idem:k1", "o1"))

			existing, err = s.Begin(t.Context(), "svc:idem:k1", "h2", "o3", time.Hour)
			require.NoError(t, err)
			assert.Equal(t, done, existing)

			_, err = s.Begin(t.Context(), "svc:idem:k2", "h1", "o4", time.Hour)
			require.NoError(t, err)
			require.NoError(t, s.Release(t.Context(), "svc:idem:k2", "o4"))

			existing, err = s.Begin(t.Context(), "svc:idem:k2", "h1", "o5", time.Hour)
			require.NoError(t, err)
			assert.Nil(t, existing)
		})
	}
}

func TestIdempotencyClaimExpiresWithItsTTL(t *testing.T) {
	t.Parallel()

	s := redis.NewIdempotency(newClient(t))

	existing, err := s.Begin(t.Context(), "svc:idem:short", "h1", "o1", 100*time.Millisecond)
	require.NoError(t, err)
	assert.Nil(t, existing)

	require.Eventually(t, func() bool {
		existing, err := s.Begin(t.Context(), "svc:idem:short", "h1", "o2", time.Hour)

		return err == nil && existing == nil
	}, 2*time.Second, 20*time.Millisecond)
}

func TestIdempotencyExpiredClaimCannotBeCompletedByItsOldOwner(t *testing.T) {
	t.Parallel()

	client := newClient(t)
	s := redis.NewIdempotency(client)
	done := &repo.IdempotencyRecord{Hash: "h1", Done: true, Status: 201, ContentType: "application/json", Body: []byte(`{}`)}

	_, err := s.Begin(t.Context(), "svc:idem:old", "h1", "o1", 100*time.Millisecond)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		existing, err := s.Begin(t.Context(), "svc:idem:old", "h1", "o2", time.Hour)

		return err == nil && existing == nil
	}, 2*time.Second, 20*time.Millisecond)

	require.ErrorIs(t, s.Complete(t.Context(), "svc:idem:old", "o1", done, 24*time.Hour), repo.ErrIdempotencyClaimLost)
	require.NoError(t, s.Complete(t.Context(), "svc:idem:old", "o2", done, 24*time.Hour))

	ttl, err := client.PTTL(t.Context(), "svc:idem:old").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 23*time.Hour)
}
