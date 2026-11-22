package clientip_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/clientip"
)

func request(t *testing.T, peer string, forwarded ...string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = peer

	for _, f := range forwarded {
		r.Header.Add("X-Forwarded-For", f)
	}

	return r
}

func TestHopCountTakesEntriesFromTheRight(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		hops string
		want string
	}{
		{"0", "10.0.0.1"},
		{"1", "2.2.2.2"},
		{"2", "1.1.1.1"},
		{"5", "1.1.1.1"},
	} {
		p, err := clientip.Parse(c.hops)
		require.NoError(t, err)
		assert.Equal(t, c.want, p.ClientIP(request(t, "10.0.0.1:5000", "1.1.1.1", "2.2.2.2")), c.hops)
	}
}

func TestCIDRsSkipTrustedHops(t *testing.T) {
	t.Parallel()

	p, err := clientip.Parse("10.0.0.0/8, 192.168.1.1")
	require.NoError(t, err)

	assert.Equal(t, "1.1.1.1", p.ClientIP(request(t, "10.0.0.1:5000", "1.1.1.1, 192.168.1.1")))
	assert.Equal(t, "3.3.3.3", p.ClientIP(request(t, "3.3.3.3:5000", "1.1.1.1")))
	assert.Equal(t, "1.1.1.1", p.ClientIP(request(t, "10.0.0.1:5000", "1.1.1.1")))
	assert.Equal(t, "10.0.0.1", p.ClientIP(request(t, "10.0.0.1:5000")))
}

func TestParseRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"-1", "10.0.0.0/33", "a,b", "10.0.0.0/8,"} {
		_, err := clientip.Parse(s)
		assert.Error(t, err, s)
	}
}

func TestSubjectTruncatesIPv6To64Bits(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "203.0.113.7", clientip.Subject("203.0.113.7"))
	assert.Equal(t, "203.0.113.7", clientip.Subject("::ffff:203.0.113.7"))
	assert.Equal(t, "2001:db8:1:2::/64", clientip.Subject("2001:db8:1:2:aaaa:bbbb:cccc:dddd"))
}
