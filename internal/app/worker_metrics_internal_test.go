package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/metrics"
)

func TestWorkerMetricsHandlerServesTheRegistryBehindTheToken(t *testing.T) {
	t.Parallel()

	var cfg config.Config

	cfg.Metrics.Path = "metrics"
	cfg.Metrics.Token = config.Secret("worker-metrics-token-0123")

	m := metrics.New("app", true)
	m.QueueJobs.WithLabelValues("mail", "completed").Inc()

	handler := workerMetricsHandler(&cfg, m)

	forbidden := httptest.NewRecorder()
	handler.ServeHTTP(forbidden, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))
	assert.Equal(t, http.StatusForbidden, forbidden.Code)
	assert.JSONEq(t, `{"code":403,"message":"Forbidden resource","data":null}`, forbidden.Body.String())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody)
	req.Header.Set("Authorization", "Bearer worker-metrics-token-0123")

	ok := httptest.NewRecorder()
	handler.ServeHTTP(ok, req)
	assert.Equal(t, http.StatusOK, ok.Code)
	assert.Contains(t, ok.Body.String(), `queue_jobs_total{app="app",queue_name="mail",status="completed"} 1`)

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/other", http.NoBody))
	assert.Equal(t, http.StatusNotFound, missing.Code)
}
