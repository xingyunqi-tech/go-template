package sms_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
)

type stored struct {
	Code      string `json:"code"`
	Attempts  int    `json:"attempts"`
	CreatedAt int64  `json:"createdAt"`
}

func TestVerifyConsumesCorrectCode(t *testing.T) {
	t.Parallel()

	cache := redis.NewMemory()
	codes := sms.NewCodes(cache)
	key := sms.CacheKey("13800138000", "login")
	require.NoError(t, cache.Set(t.Context(), key, stored{Code: "123456", Attempts: 0, CreatedAt: time.Now().UnixMilli()}, time.Minute))

	ok, err := codes.Verify(t.Context(), "13800138000", "000000", "login")
	require.NoError(t, err)
	assert.False(t, ok)

	var s stored

	found, _ := cache.Get(t.Context(), key, &s)
	require.True(t, found)
	assert.Equal(t, 1, s.Attempts)

	ok, err = codes.Verify(t.Context(), "13800138000", "123456", "login")
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = codes.Verify(t.Context(), "13800138000", "123456", "login")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestVerifyDiscardsCodeAfterTooManyAttempts(t *testing.T) {
	t.Parallel()

	cache := redis.NewMemory()
	codes := sms.NewCodes(cache)
	key := sms.CacheKey("13800138000", "verify_identity")
	require.NoError(t, cache.Set(t.Context(), key, stored{Code: "123456", Attempts: sms.MaxAttempts, CreatedAt: time.Now().UnixMilli()}, time.Minute))

	ok, err := codes.Verify(t.Context(), "13800138000", "123456", "verify_identity")
	require.NoError(t, err)
	assert.False(t, ok)

	var s stored

	found, _ := cache.Get(t.Context(), key, &s)
	assert.False(t, found)
}
