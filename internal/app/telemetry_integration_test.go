//go:build integration

package app_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

type sink struct {
	mu     sync.Mutex
	bodies map[string][]byte
}

func newSink(t *testing.T) (s *sink, url string) {
	t.Helper()

	s = &sink{mu: sync.Mutex{}, bodies: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		s.mu.Lock()
		s.bodies[r.URL.Path] = append(s.bodies[r.URL.Path], body...)
		s.mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	url = srv.URL

	return s, url
}

func (s *sink) received(path string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	for p, b := range s.bodies {
		if strings.HasSuffix(p, path) {
			return b
		}
	}

	return nil
}

func TestTracesLogsAndErrorsAreExported(t *testing.T) {
	otlp, otlpURL := newSink(t)
	sentrySink, sentryURL := newSink(t)

	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_LOGS_EXPORTER", "otlp")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", otlpURL)
	t.Setenv("OTEL_SERVICE_NAME", "traced-app")

	cfg := loadConfig(t, _db.Clone(t))
	cfg.Telemetry.MetricsExporter = config.MetricsNone
	cfg.Telemetry.SentryDSN = config.Secret(strings.Replace(sentryURL, "http://", "http://public@", 1) + "/1")

	obs, err := app.SetupObservability(t.Context(), cfg, "test")
	require.NoError(t, err)

	log := obs.Logger(slog.New(slog.DiscardHandler))
	log.InfoContext(t.Context(), "exported log line")

	infra, err := app.OpenInfra(t.Context(), cfg, 0)
	require.NoError(t, err)
	t.Cleanup(infra.Close)

	handler, err := app.NewHandler(t.Context(), cfg, log, infra, nil)
	require.NoError(t, err)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	a := &testApp{t: t, url: srv.URL, cfg: cfg, infra: infra}
	a.get("/health/live", "")

	infra.Pool.Close()

	failed := a.do(http.MethodPost, "/api/v1/notes", testutil.IssueAccessToken(t, nil), `{"title":"t"}`, "X-Request-Id", "req-sentry")
	require.GreaterOrEqual(t, failed.Status, http.StatusInternalServerError, failed.Raw)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, obs.Shutdown(ctx))

	traces := otlp.received("/v1/traces")
	require.NotEmpty(t, traces)
	assert.True(t, bytes.Contains(traces, []byte("GET /health/live")))
	assert.True(t, bytes.Contains(traces, []byte("traced-app")))
	assert.True(t, bytes.Contains(otlp.received("/v1/logs"), []byte("exported log line")))

	require.Eventually(t, func() bool { return len(sentrySink.received("/envelope/")) > 0 }, 5*time.Second, 50*time.Millisecond)
	assert.True(t, bytes.Contains(sentrySink.received("/envelope/"), []byte("req-sentry")))
}
