//go:build integration

package app_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
)

const _testMonitorToken = "monitor-token-for-tests"

func setFlag(t *testing.T, f *config.DisabledFlag, value string) {
	t.Helper()

	require.NoError(t, f.UnmarshalText([]byte(value)))
}

func throttled(limit int) func(*config.Config) {
	return func(c *config.Config) {
		c.Throttle.Limit = limit
		c.Throttle.TTL = 60
	}
}

func TestProbesAreTerminusShaped(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	live := a.do(http.MethodGet, "/api/v1/health/live", "", nil)
	if live.Status == http.StatusServiceUnavailable {
		assert.Greater(t, health.RuntimeMemory(), uint64(300*1024*1024), "liveness only fails above the heap threshold")
		assert.JSONEq(t, `{"code":503,"message":"SERVICE_UNAVAILABLE","data":null}`, live.Raw)

		return
	}

	require.Equal(t, http.StatusOK, live.Status, live.Raw)
	assert.Equal(t, "no-cache, no-store, must-revalidate", live.Header.Get("Cache-Control"))
	assert.JSONEq(t, `{"code":200,"message":"success","data":{"status":"ok","info":{"memory_heap":{"status":"up"}},`+
		`"error":{},"details":{"memory_heap":{"status":"up"}}}}`, live.Raw)

	ready := a.do(http.MethodGet, "/api/v1/health/ready", "", nil)
	require.Equal(t, http.StatusOK, ready.Status, ready.Raw)

	database, _ := ready.data()["info"].(map[string]any)["database"].(map[string]any)
	assert.Equal(t, "up", database["status"])
	assert.IsType(t, float64(0), database["responseTime"])
}

func TestFullHealthCheckNeedsTheMonitorToken(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, func(c *config.Config) { c.Metrics.Token = config.Secret(_testMonitorToken) }, nil)

	denied := a.do(http.MethodGet, "/api/v1/health", "", nil)
	assert.Equal(t, http.StatusForbidden, denied.Status)
	assert.JSONEq(t, `{"code":403,"message":"Forbidden resource","data":null}`, denied.Raw)

	r := a.do(http.MethodGet, "/api/v1/health", _testMonitorToken, nil)
	require.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, r.Status, r.Raw)

	if r.Status == http.StatusOK {
		assert.True(t, strings.Index(r.Raw, `"memory_heap"`) < strings.Index(r.Raw, `"disk"`))
		assert.True(t, strings.Index(r.Raw, `"database_pool"`) < strings.Index(r.Raw, `"wechat_api"`))
		assert.Contains(t, r.Raw, `"wechat_api":{"message":"WeChat not configured, skipped","status":"up"}`)
		assert.NotContains(t, r.Raw, `"redis"`)
	}
}

func TestFailedProbeIsServiceUnavailable(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	a.pool.Close()

	r := a.do(http.MethodGet, "/api/v1/health/ready", "", nil)

	assert.Equal(t, http.StatusServiceUnavailable, r.Status)
	assert.JSONEq(t, `{"code":503,"message":"SERVICE_UNAVAILABLE","data":null}`, r.Raw)
	assert.Empty(t, r.Header.Get("Cache-Control"))
}

func TestMetricsEndpoint(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, func(c *config.Config) {
		c.Metrics.Token = config.Secret(_testMonitorToken)
		c.App.Name = "metrics-app"
	}, nil)

	a.do(http.MethodGet, "/api/v1/users/7", "", nil)

	assert.Equal(t, http.StatusForbidden, a.do(http.MethodGet, "/metrics", "", nil).Status)

	r := a.do(http.MethodGet, "/metrics", _testMonitorToken, nil)
	require.Equal(t, http.StatusOK, r.Status)
	assert.Equal(t, "text/plain; version=0.0.4; charset=utf-8", r.Header.Get("Content-Type"))
	assert.Contains(t, r.Raw, `http_requests_total{app="metrics-app",method="GET",path="/api/v1/users/:id",status="401"} 1`)
	assert.Contains(t, r.Raw, `http_requests_total{app="metrics-app",method="GET",path="/metrics",status="403"} 1`)
}

func TestThrottleHeadersAndRejection(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, throttled(2), nil)

	for i := range 2 {
		r := a.do(http.MethodGet, "/", "", nil)
		require.Equal(t, http.StatusOK, r.Status)
		assert.Equal(t, "2", r.Header.Get("X-RateLimit-Limit"))
		assert.Equal(t, strconv.Itoa(1-i), r.Header.Get("X-RateLimit-Remaining"))
		assert.NotEmpty(t, r.Header.Get("X-RateLimit-Reset"))
	}

	r := a.do(http.MethodGet, "/", "", nil)
	assert.Equal(t, http.StatusTooManyRequests, r.Status)
	assert.Equal(t, "Too many requests, please try again later", r.Body["message"])
	assert.NotEmpty(t, r.Header.Get("Retry-After"))
	assert.Empty(t, r.Header.Get("X-RateLimit-Limit"))

	unmatched := a.do(http.MethodGet, "/api/nope", "", nil)
	assert.Equal(t, http.StatusNotFound, unmatched.Status)
	assert.Empty(t, unmatched.Header.Get("X-RateLimit-Limit"))

	login := a.do(http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": "nobody@example.com", "password": _secret00})
	assert.Equal(t, "5", login.Header.Get("X-RateLimit-Limit"))
}

func TestThrottleIsDisabledByFlag(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, func(c *config.Config) { setFlag(t, &c.Throttle.Disabled, "false") }, nil)

	assert.Empty(t, a.do(http.MethodGet, "/", "", nil).Header.Get("X-RateLimit-Limit"))
}

func TestThrottleCountsJWTRoutesPerUser(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, throttled(1), nil)

	admin := loginOnce(t, a, "admin@example.com")
	john := loginOnce(t, a, "john.doe@example.com")

	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/auth/me", admin, nil).Status)
	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/auth/me", john, nil).Status)
	assert.Equal(t, http.StatusTooManyRequests, a.do(http.MethodGet, "/api/v1/auth/me", admin, nil).Status)
}

func loginOnce(t *testing.T, a *testApp, email string) string {
	t.Helper()

	r := a.do(http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": email, "password": _secret00},
		"X-Forwarded-For", email)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	token, _ := r.data()["token"].(string)

	return token
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p) //nolint:wrapcheck // in-memory
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func TestThrottleFallsBackToMemoryWhenRedisFails(t *testing.T) {
	t.Parallel()

	logs := &syncBuffer{}
	a := newAppWith(t, 0, func(c *config.Config) {
		throttled(1)(c)
		c.Metrics.Token = config.Secret(_testMonitorToken)
		require.NoError(t, c.Redis.Enabled.UnmarshalText([]byte("true")))
		c.Redis.Host = "127.0.0.1"
		c.Redis.Port = 1
	}, slog.New(slog.NewJSONHandler(logs, nil)))

	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, "/", "", nil).Status)
	assert.Equal(t, http.StatusTooManyRequests, a.do(http.MethodGet, "/", "", nil).Status)

	metrics := a.do(http.MethodGet, "/metrics", _testMonitorToken, nil)
	assert.Contains(t, metrics.Raw, `throttler_storage_fallback_total{app="app"} 3`)
	assert.Equal(t, 1, strings.Count(logs.String(), "redis throttler storage unavailable"))
	assert.Contains(t, metrics.Raw, `admin_alerts_total{app="app",level="CRITICAL",type="throttler"} 1`)

	var alertType, level, dedupKey string

	require.Eventually(t, func() bool {
		return a.pool.QueryRow(t.Context(), `SELECT "type", "level", "dedupKey" FROM "admin_alert"`).
			Scan(&alertType, &level, &dedupKey) == nil
	}, 5*time.Second, 20*time.Millisecond)
	assert.Equal(t, []string{"throttler", "CRITICAL", "throttler-storage-fallback"}, []string{alertType, level, dedupKey})

	var title, notificationType, channels string

	require.Eventually(t, func() bool {
		var n int

		_ = a.pool.QueryRow(t.Context(), `SELECT count(*) FROM "notification"`).Scan(&n)

		return n > 0
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT n."title", n."type", n."channels"::text FROM "notification" AS n
		JOIN "user" AS u ON u."id" = n."userId" WHERE u."email" = 'admin@example.com'`).Scan(&title, &notificationType, &channels))
	assert.Equal(t, []string{"[CRITICAL] throttler", "ERROR", `["IN_APP"]`}, []string{title, notificationType, channels})
}
