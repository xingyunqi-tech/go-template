package pagination_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

func TestNewComputesTotalPages(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(pagination.New([]int{1, 2}, 21, 2, 10))
	require.NoError(t, err)
	assert.JSONEq(t, `{"list":[1,2],"total":21,"page":2,"limit":10,"totalPages":3}`, string(out))

	out, err = json.Marshal(pagination.New[int](nil, 0, 1, 10))
	require.NoError(t, err)
	assert.JSONEq(t, `{"list":[],"total":0,"page":1,"limit":10,"totalPages":0}`, string(out))
}

func TestNewInfinityReportsNextPage(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(pagination.NewInfinity([]string{"a", "b"}, 2))
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":["a","b"],"hasNextPage":true}`, string(out))

	out, err = json.Marshal(pagination.NewInfinity[string](nil, 2))
	require.NoError(t, err)
	assert.JSONEq(t, `{"data":[],"hasNextPage":false}`, string(out))
}

func TestOffsetLimitRoundsLikePostgres(t *testing.T) {
	t.Parallel()

	offset, limit := pagination.OffsetLimit(3, 10)
	assert.Equal(t, int64(20), offset)
	assert.Equal(t, int64(10), limit)

	offset, limit = pagination.OffsetLimit(1.5, 5)
	assert.Equal(t, int64(3), offset)
	assert.Equal(t, int64(5), limit)
}

func TestLazyTotal(t *testing.T) {
	t.Parallel()

	total, ok := pagination.LazyTotal(3, 2, 10)
	assert.True(t, ok)
	assert.InDelta(t, 13, total, 0)

	total, ok = pagination.LazyTotal(3, 1, 10)
	assert.True(t, ok)
	assert.InDelta(t, 3, total, 0)

	_, ok = pagination.LazyTotal(10, 1, 10)
	assert.False(t, ok)

	_, ok = pagination.LazyTotal(0, 2, 10)
	assert.False(t, ok)
}
