//go:build integration

package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/pkg/telemetry"
)

const _metricsToken = "metrics-token-for-tests" //nolint:gosec // test token

func TestReadinessChecksDatabaseAndRedis(t *testing.T) {
	t.Parallel()

	redisDB := _redis.Borrow(t)
	a := newApp(t, func(c *config.Config) { c.Redis.URL = config.Secret(redisDB.URL()) }, nil)

	live := a.get("/health/live", "")
	assert.Equal(t, http.StatusOK, live.Status)

	ready := a.get("/health/ready", "")
	require.Equal(t, http.StatusOK, ready.Status, ready.Raw)
	assert.Equal(t, map[string]any{"database": "ok", "redis": "ok"}, ready.Body["checks"])

	a.infra.Pool.Close()

	down := a.get("/health/ready", "")
	assert.Equal(t, http.StatusServiceUnavailable, down.Status)
	assert.Equal(t, map[string]any{"database": "error", "redis": "ok"}, down.Body["checks"])
	assert.NotEmpty(t, down.Header.Get("Retry-After"))
	assert.Equal(t, http.StatusOK, a.get("/health/live", "").Status)
}

func TestMetricsAreScrapedWithTheToken(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	provider, err := telemetry.Setup(t.Context(), telemetry.Options{Disabled: false, MetricsExporter: telemetry.MetricsPrometheus})
	require.NoError(t, err)
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()) })

	handler := promhttp.HandlerFor(provider.Registry, promhttp.HandlerOpts{}) //nolint:exhaustruct // defaults
	a := newApp(t, func(c *config.Config) { c.Metrics.Token = _metricsToken }, handler)

	a.get("/health/live", "")
	a.get("/api/v1/nope", "")

	assert.Equal(t, http.StatusUnauthorized, a.get("/metrics", "").Status)

	r := a.get("/metrics", _metricsToken)
	require.Equal(t, http.StatusOK, r.Status)

	var line string

	for l := range strings.SplitSeq(r.Raw, "\n") {
		if strings.HasPrefix(l, "http_server_request_duration_seconds_count{") && strings.Contains(l, `http_route="/health/live"`) {
			line = l
		}
	}

	require.NotEmpty(t, line, r.Raw)
	assert.Contains(t, line, `http_request_method="GET"`)
	assert.Contains(t, line, `http_response_status_code="200"`)
	assert.Contains(t, r.Raw, "db_client_connection")
}
