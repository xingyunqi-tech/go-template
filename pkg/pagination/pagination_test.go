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
