package pagination_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

func TestCursorRoundTrip(t *testing.T) {
	t.Parallel()

	c := pagination.Cursor{Key: "2026-11-17T08:00:00.000Z", ID: "019a8f3c-6b1e-7d2a-9c4f-1e2d3c4b5a69"}

	got, err := pagination.DecodeCursor(c.Encode())
	require.NoError(t, err)
	assert.Equal(t, c, got)
	assert.NotContains(t, c.Encode(), "=")

	for _, bad := range []string{"", "!!", "e30", "bm90IGpzb24"} {
		_, err := pagination.DecodeCursor(bad)
		require.ErrorIs(t, err, pagination.ErrInvalidCursor, bad)
	}
}

func TestCursorPageUsesTheExtraRow(t *testing.T) {
	t.Parallel()

	cursorOf := func(n int) pagination.Cursor { return pagination.Cursor{Key: "", ID: string("abcd"[n])} }

	full := pagination.NewCursorPage([]int{1, 2, 3}, 2, cursorOf)
	assert.Equal(t, []int{1, 2}, full.Items)
	require.NotNil(t, full.NextCursor)

	next, err := pagination.DecodeCursor(*full.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, "c", next.ID)

	last := pagination.NewCursorPage([]int{1}, 2, cursorOf)
	assert.Nil(t, last.NextCursor)

	empty := pagination.NewCursorPage[int](nil, 2, cursorOf)
	assert.Equal(t, []int{}, empty.Items)
}

func TestParseSort(t *testing.T) {
	t.Parallel()

	allowed := []string{"createdAt", "email"}

	got, err := pagination.ParseSort("email,-createdAt", "-createdAt", allowed)
	require.NoError(t, err)
	assert.Equal(t, []pagination.Sort{{Field: "email", Descending: false}, {Field: "createdAt", Descending: true}}, got)

	got, err = pagination.ParseSort("", "-createdAt", allowed)
	require.NoError(t, err)
	assert.Equal(t, []pagination.Sort{{Field: "createdAt", Descending: true}}, got)

	for _, bad := range []string{"name", "email,", "--email"} {
		_, err := pagination.ParseSort(bad, "-createdAt", allowed)
		require.ErrorIs(t, err, pagination.ErrInvalidSort, bad)
	}
}

func TestPageNumbers(t *testing.T) {
	t.Parallel()

	p := pagination.NewPage[string](nil, 41, 3, 20)
	assert.Equal(t, []string{}, p.Items)
	assert.Equal(t, 40, pagination.Offset(3, 20))
}
