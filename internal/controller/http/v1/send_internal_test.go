package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRangeFollowsRangeParser(t *testing.T) {
	t.Parallel()

	cases := []struct {
		header string
		ranges []byteRange
		code   int
	}{
		{"bytes=abc", nil, _rangeInvalid},
		{"bytes", nil, _rangeInvalid},
		{"bytes=0-1,x", nil, _rangeInvalid},
		{"bytes=5-2", nil, _rangeUnsatisfiable},
		{"bytes=100-200", nil, _rangeUnsatisfiable},
		{"bytes=-0", nil, _rangeUnsatisfiable},
		{"bytes=-3", []byteRange{{8, 10}}, 0},
		{"bytes=3-", []byteRange{{3, 10}}, 0},
		{"bytes=5-100", []byteRange{{5, 10}}, 0},
		{"bytes= 0 - 1 ", []byteRange{{0, 1}}, 0},
		{"bytes=0-1,1-2", []byteRange{{0, 2}}, 0},
		{"bytes=4-5,0-1", []byteRange{{4, 5}, {0, 1}}, 0},
		{"bytes=4-5,0-1,2-3", []byteRange{{0, 5}}, 0},
		{"bytes=5-2,0-1", []byteRange{{0, 1}}, 0},
	}

	for _, c := range cases {
		ranges, code := parseRange(11, c.header)
		assert.Equal(t, c.code, code, c.header)
		assert.Equal(t, c.ranges, ranges, c.header)
	}
}

func TestParseTokenListSkipsEmptyTokens(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{`"a"`, `W/"b"`}, parseTokenList(` "a" ,, W/"b" `))
}

func TestJoinedJoinsRepeatedHeaders(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Add("If-None-Match", `"a"`)
	r.Header.Add("If-None-Match", `"b"`)

	assert.Equal(t, `"a", "b"`, joined(r, "If-None-Match"))
	assert.Empty(t, joined(r, "Range"))
}
