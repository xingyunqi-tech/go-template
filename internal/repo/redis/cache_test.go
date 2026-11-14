package redis_test

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
)

func TestMemoryExpiresAndDeletes(t *testing.T) {
	t.Parallel()

	m := redis.NewMemory()
	require.NoError(t, m.Set(t.Context(), "k", map[string]int{"a": 1}, time.Hour))

	var got map[string]int

	found, err := m.Get(t.Context(), "k", &got)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, map[string]int{"a": 1}, got)

	require.NoError(t, m.Set(t.Context(), "short", 1, time.Nanosecond))
	time.Sleep(time.Millisecond)

	var n int

	found, err = m.Get(t.Context(), "short", &n)
	require.NoError(t, err)
	assert.False(t, found)

	require.NoError(t, m.Delete(t.Context(), "k"))

	found, err = m.Get(t.Context(), "k", &got)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestMemoryEvictsOldestWhenFull(t *testing.T) {
	t.Parallel()

	m := redis.NewMemory()
	for i := range 10_001 {
		require.NoError(t, m.Set(t.Context(), strconv.Itoa(i), i, time.Hour))
	}

	var n int

	found, _ := m.Get(t.Context(), "0", &n)
	assert.False(t, found)

	found, _ = m.Get(t.Context(), "10000", &n)
	assert.True(t, found)

	for i := range 30_000 {
		require.NoError(t, m.Set(t.Context(), "k", i, time.Hour))
		require.NoError(t, m.Delete(t.Context(), "k"))
	}
}

func TestCacheWithoutRedisUsesMemory(t *testing.T) {
	t.Parallel()

	c := redis.NewCache(nil, slog.New(slog.DiscardHandler))
	require.NoError(t, c.Set(t.Context(), "k", "v", time.Hour))

	var got string

	found, err := c.Get(t.Context(), "k", &got)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "v", got)
	require.NoError(t, c.Delete(t.Context(), "k"))
}

func TestMemoryStoresJSONWithoutHTMLEscaping(t *testing.T) {
	t.Parallel()

	m := redis.NewMemory()
	require.NoError(t, m.Set(t.Context(), "k", map[string]string{"a": "<&>"}, time.Hour))

	var raw json.RawMessage

	found, err := m.Get(t.Context(), "k", &raw)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, `{"a":"<&>"}`, string(raw))
}
