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
	t.Parallel()

	otlp, otlpURL := newSink(t)
	sentrySink, sentryURL := newSink(t)

	a := newAppWith(t, 0, func(c *config.Config) {
		require.NoError(t, c.Observability.OTelEnabled.UnmarshalText([]byte("true")))
		c.Observability.OTelEndpoint = otlpURL
		c.Observability.SentryDSN = config.Secret(strings.Replace(sentryURL, "http://", "http://public@", 1) + "/1")
		c.App.Name = "traced-app"
	}, nil)

	obs, err := app.SetupObservability(t.Context(), a.cfg, "test")
	require.NoError(t, err)

	log := obs.Logger(slog.New(slog.DiscardHandler))
	log.InfoContext(t.Context(), "exported log line")

	token := a.login("john.doe@example.com", _secret00).access
	a.do(http.MethodGet, "/api/v1/users/12", token, nil)

	a.pool.Close()
	r := a.do(http.MethodGet, "/api/v1/auth/me", token, nil)
	require.GreaterOrEqual(t, r.Status, http.StatusInternalServerError, r.Raw)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	require.NoError(t, obs.Shutdown(ctx))

	traces := otlp.received("/v1/traces")
	require.NotEmpty(t, traces)
	assert.True(t, bytes.Contains(traces, []byte("GET /api/v1/users/:id")))
	assert.True(t, bytes.Contains(traces, []byte("traced-app")))
	assert.True(t, bytes.Contains(otlp.received("/v1/logs"), []byte("exported log line")))

	require.Eventually(t, func() bool { return len(sentrySink.received("/envelope/")) > 0 }, 5*time.Second, 50*time.Millisecond)
	assert.True(t, bytes.Contains(sentrySink.received("/envelope/"), []byte(`"exception"`)))
}
