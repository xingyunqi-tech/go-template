package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

func TestNormalizeIPFollowsNestjs(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"203.0.113.7":              "203.0.113.7",
		"":                         "",
		"not-an-ip":                "not-an-ip",
		"::1":                      "::1",
		"0:0:0:0:0:0:0:1":          "::1",
		"::ffff:198.51.100.4":      "198.51.100.4",
		"2001:db8:abcd:12:1:2:3:4": "2001:db8:abcd:12::/64",
		"2001:DB8::1%eth0":         "2001:db8::/64",
		"2001:db8:0:0:1::":         "2001:db8::/64",
	}

	for in, want := range cases {
		assert.Equal(t, want, NormalizeIP(in), in)
	}
}

func newTestThrottler(t *testing.T, driver string, limiter Limiter) *throttler {
	t.Helper()

	th, err := newThrottler(ThrottleOptions{
		Enabled: true, Limit: 100, TTL: time.Minute, AccessSecret: "secret", FileDriver: driver, Limiter: limiter,
	}, openapi.NestHandlers, 1)
	require.NoError(t, err)

	return th
}

func TestThrottleKeysMatchNestjs(t *testing.T) {
	t.Parallel()

	th := newTestThrottler(t, "local", nil)
	assert.Equal(t, "throttle:1fd634fe207bfc8d5fe7cd19c9728810c4d04bb363103764589b42fa91658d61:default:gcra", th.key("loginWithEmail", "ip:203.0.113.7"))
	assert.Equal(t, "throttle:c969c171de5bfbd5a287d2bc810079ebda5a7dc2081a53e0ca27e523f2dd4d65:default:gcra", th.key("getCurrentUser", "user:2"))
	assert.Equal(t, "throttle:93eb39f0a7156f09b5b102255c9d92f14a238365276d24c92689bddbeb0b5613:default:gcra",
		newTestThrottler(t, "s3", nil).key("uploadFile", "ip:1.1.1.1"))
	assert.Equal(t, "unknownOperation", th.handlerName("unknownOperation"))
	assert.Equal(t, "FilesS3PresignedController-uploadFile", newTestThrottler(t, "s3-presigned", nil).handlerName("uploadFile"))
}

func TestTrackerCountsJWTRoutesPerUser(t *testing.T) {
	t.Parallel()

	th := newTestThrottler(t, "local", nil)
	token, err := jwtx.Sign(map[string]any{"id": 7, "sessionId": 1}, "secret", 60_000, time.Now())
	require.NoError(t, err)

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = "10.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "::ffff:198.51.100.4")
	r.Header.Set("Authorization", "Bearer "+token)

	assert.Equal(t, "user:7", th.tracker(r, &Policy{Auth: AuthJWT}))
	assert.Equal(t, "ip:198.51.100.4", th.tracker(r, &Policy{Auth: AuthOptionalJWT}))

	r.Header.Set("Authorization", "Bearer forged")
	assert.Equal(t, "ip:198.51.100.4", th.tracker(r, &Policy{Auth: AuthJWT}))
}

type countingLimiter struct {
	memory *ratelimit.Memory
}

func (c countingLimiter) Consume(_ context.Context, key string, period time.Duration, limit int) ratelimit.Decision {
	return c.memory.Consume(key, period.Microseconds(), limit, 0)
}

func TestThrottleHeadersAndRejection(t *testing.T) {
	t.Parallel()

	th := newTestThrottler(t, "local", countingLimiter{memory: ratelimit.NewMemory()})
	policy := &Policy{Auth: AuthNone, Throttle: &Throttle{Limit: 2, TTL: 60_000}}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)

	rec := httptest.NewRecorder()
	require.NoError(t, th.check(rec, r, "loginWithEmail", policy))
	assert.Equal(t, "2", rec.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "1", rec.Header().Get("X-RateLimit-Remaining"))
	assert.Equal(t, "30", rec.Header().Get("X-RateLimit-Reset"))

	require.NoError(t, th.check(httptest.NewRecorder(), r, "loginWithEmail", policy))

	err := th.check(httptest.NewRecorder(), r, "loginWithEmail", policy)

	var tooMany *httperr.TooManyRequests
	require.ErrorAs(t, err, &tooMany)
	assert.Equal(t, "30", tooMany.Headers.Get("Retry-After"))

	rec = httptest.NewRecorder()
	require.NoError(t, th.check(rec, r, "getAppInfo", &Policy{Auth: AuthNone}))
	assert.Equal(t, "100", rec.Header().Get("X-RateLimit-Limit"))

	th.opts.Enabled = false
	rec = httptest.NewRecorder()
	require.NoError(t, th.check(rec, r, "loginWithEmail", policy))
	assert.Empty(t, rec.Header().Get("X-RateLimit-Limit"))
}
