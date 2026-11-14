package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	v1 "github.com/xingyunqi-tech/go-template/internal/controller/http/v1"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/metrics"
)

const _monitorToken = "monitor-token-0123456789"

func metricsRouter(t *testing.T, enabled, production bool, token string) http.Handler {
	t.Helper()

	opts := options(t)
	opts.Production = production
	opts.MonitorToken = token
	opts.Metrics = metrics.New("demo", enabled)
	opts.MetricsPath = "metrics"

	h, err := httpapi.NewRouter(v1.New(&v1.Deps{
		AppName: "demo", Auth: nil, Users: nil, URLs: view.FileURLs{BackendDomain: "", APIPrefix: "api"}, Health: nil, Log: nil,
	}), opts)
	require.NoError(t, err)

	return h
}

func scrape(t *testing.T, h http.Handler) string {
	t.Helper()

	rec := do(t, h, http.MethodGet, "/metrics", http.NoBody, map[string]string{"Authorization": "Bearer " + _monitorToken})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, metrics.ContentType, rec.Header().Get("Content-Type"))

	return rec.Body.String()
}

func TestMetricsRecordExpressRouteLabels(t *testing.T) {
	t.Parallel()

	h := metricsRouter(t, true, false, _monitorToken)

	serve(t, h, http.MethodGet, "/API/v1/users/12/")
	serve(t, h, http.MethodGet, "/api/nope")
	serve(t, h, http.MethodGet, "/")
	do(t, h, http.MethodOptions, "/api/v1/users", http.NoBody, map[string]string{
		"Origin": "http://localhost:5173", "Access-Control-Request-Method": "GET",
	})

	body := scrape(t, h)

	assert.Contains(t, body, `http_requests_total{app="demo",method="GET",path="/api/v1/users/:id",status="401"} 1`)
	assert.Contains(t, body, `http_requests_total{app="demo",method="GET",path="<unmatched>",status="404"} 1`)
	assert.Contains(t, body, `http_requests_total{app="demo",method="GET",path="/",status="200"} 1`)
	assert.Contains(t, body, `http_request_duration_seconds_bucket{app="demo",method="GET",path="/",status="200",le="0.005"}`)
	assert.Contains(t, body, `http_active_connections{app="demo"} 1`)
	assert.Contains(t, body, "# HELP throttler_storage_fallback_total Total number of rate limit checks served by the in-memory storage because Redis was unavailable")
	assert.Contains(t, body, "go_goroutines")
	assert.NotContains(t, body, `method="OPTIONS"`)
}

// brokenWriter fails every write like a connection the client closed.
type brokenWriter struct {
	*httptest.ResponseRecorder
}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestMetricsRecordClientClosedRequests(t *testing.T) {
	t.Parallel()

	h := metricsRouter(t, true, false, _monitorToken)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)
	h.ServeHTTP(brokenWriter{httptest.NewRecorder()}, req)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody))
	assert.Contains(t, scrape(t, h), `http_requests_total{app="demo",method="GET",path="/",status="200"} 1`)

	assert.Contains(t, scrape(t, h), `http_requests_total{app="demo",method="GET",path="/",status="499"} 1`)
}

func TestDisabledMetricsKeepTheRouteWithoutRequestSamples(t *testing.T) {
	t.Parallel()

	h := metricsRouter(t, false, false, _monitorToken)
	serve(t, h, http.MethodGet, "/")

	body := scrape(t, h)

	assert.NotContains(t, body, "http_requests_total{")
	assert.NotContains(t, body, "go_goroutines")
	assert.Contains(t, body, `http_active_connections{app="demo"} 0`)
}

func TestMonitorToken(t *testing.T) {
	t.Parallel()

	forbidden := `{"code":403,"message":"Forbidden resource","data":null}`
	cases := []struct {
		name          string
		production    bool
		token, header string
		allowed       bool
	}{
		{"matching bearer", true, _monitorToken, "Bearer " + _monitorToken, true},
		{"scheme is case-insensitive", true, _monitorToken, "  bEaReR   " + _monitorToken + " extra", true},
		{"wrong token", false, _monitorToken, "Bearer " + _monitorToken + "x", false},
		{"missing header", false, _monitorToken, "", false},
		{"other scheme", false, _monitorToken, "Basic " + _monitorToken, false},
		{"no token outside production", false, "", "", true},
		{"no token in production", true, "", "Bearer anything", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := metricsRouter(t, true, tc.production, tc.token)
			rec := do(t, h, http.MethodGet, "/metrics", http.NoBody, map[string]string{"Authorization": tc.header})

			if tc.allowed {
				assert.Equal(t, http.StatusOK, rec.Code)
				assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain"))

				return
			}

			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.JSONEq(t, forbidden, rec.Body.String())

			rec = do(t, h, http.MethodGet, "/api/v1/health", http.NoBody, map[string]string{"Authorization": tc.header})
			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.JSONEq(t, forbidden, rec.Body.String())
		})
	}
}

func TestMetricsPathHasNoAPIPrefix(t *testing.T) {
	t.Parallel()

	h := metricsRouter(t, true, false, "")

	assert.Equal(t, http.StatusNotFound, serve(t, h, http.MethodGet, "/api/metrics").Code)
	assert.Equal(t, http.StatusOK, serve(t, h, http.MethodGet, "/metrics").Code)
}

func TestRequestLogFollowsPinoHTTP(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	opts := options(t)
	opts.Log = slog.New(slog.NewJSONHandler(&buf, nil))

	h, err := httpapi.NewRouter(v1.New(&v1.Deps{
		AppName: "demo", Auth: nil, Users: nil, URLs: view.FileURLs{BackendDomain: "", APIPrefix: "api"}, Health: nil, Log: nil,
	}), opts)
	require.NoError(t, err)

	do(t, h, http.MethodGet, "/api/v1/users/3?token=abc&x=1&x=2", http.NoBody, map[string]string{
		"Authorization": "Bearer t", "Cookie": "a=b", "X-Request-Id": "req-1", "User-Agent": "ua",
	})
	serve(t, h, http.MethodGet, "/")

	var entries []map[string]any

	for line := range bytes.Lines(buf.Bytes()) {
		var e map[string]any
		require.NoError(t, json.Unmarshal(line, &e))

		if e["context"] == "HTTP" {
			entries = append(entries, e)
		}
	}

	require.Len(t, entries, 2)

	rejected := entries[0]
	assert.Equal(t, "GET /api/v1/users/3?token=abc&x=1&x=2 401", rejected["msg"])
	assert.Equal(t, "WARN", rejected["level"])
	assert.Equal(t, map[string]any{"statusCode": float64(401)}, rejected["res"])
	assert.IsType(t, float64(0), rejected["responseTime"])

	req, _ := rejected["req"].(map[string]any)
	assert.Equal(t, "req-1", req["id"])
	assert.Equal(t, "GET", req["method"])
	assert.Equal(t, "/api/v1/users/3?token=abc&x=1&x=2", req["url"])
	assert.Equal(t, map[string]any{"token": "[REDACTED]", "x": "2"}, req["query"])
	assert.Equal(t, "192.0.2.1", req["remoteAddress"])

	headers, _ := req["headers"].(map[string]any)
	assert.Equal(t, "[REDACTED]", headers["authorization"])
	assert.Equal(t, "[REDACTED]", headers["cookie"])
	assert.Equal(t, "ua", headers["user-agent"])

	assert.Equal(t, "GET / 200", entries[1]["msg"])
	assert.Equal(t, "INFO", entries[1]["level"])
}
