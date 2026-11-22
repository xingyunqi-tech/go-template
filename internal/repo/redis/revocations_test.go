package redis_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
)

func TestRevocationsWithoutRedisStayInProcess(t *testing.T) {
	t.Parallel()

	r := redis.NewRevocations(nil)
	sid := uuid.NewV7()

	require.NoError(t, r.Revoke(t.Context(), sid, time.Minute))

	revoked, err := r.IsRevoked(t.Context(), sid)
	require.NoError(t, err)
	assert.True(t, revoked)

	revoked, err = r.IsRevoked(t.Context(), uuid.NewV7())
	require.NoError(t, err)
	assert.False(t, revoked)
}
