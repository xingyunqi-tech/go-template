// Package metrics holds the Prometheus collectors named, labeled and bucketed like the nestjs
// MetricsModule; every sample carries app=<APP_NAME>.
package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/common/expfmt"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

// ContentType is the /metrics content type (Prometheus text format 0.0.4).
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Unmatched is the path label of requests that matched no route.
const Unmatched = "<unmatched>"

// Metrics are the application collectors.
type Metrics struct {
	registry *prometheus.Registry
	enabled  bool

	HTTPRequests       *prometheus.CounterVec
	HTTPDuration       *prometheus.HistogramVec
	HTTPActive         prometheus.Gauge
	DatabaseQuery      *prometheus.HistogramVec
	CacheHits          *prometheus.CounterVec
	CacheMisses        *prometheus.CounterVec
	QueueJobs          *prometheus.CounterVec
	AdminAlerts        *prometheus.CounterVec
	AdminAlertsDeduped *prometheus.CounterVec
	ThrottlerFallbacks prometheus.Counter
}

// New registers the collectors; enabled (METRICS_ENABLED) adds the Go and process collectors and
// turns on HTTP request recording.
func New(appName string, enabled bool) *Metrics {
	registry := prometheus.NewRegistry()
	reg := prometheus.WrapRegistererWith(prometheus.Labels{"app": appName}, registry)
	factory := func(c prometheus.Collector) prometheus.Collector {
		reg.MustRegister(c)

		return c
	}

	m := &Metrics{registry: registry, enabled: enabled}

	m.HTTPRequests, _ = factory(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total", Help: "Total number of HTTP requests",
	}, []string{"method", "path", "status"})).(*prometheus.CounterVec)
	m.HTTPDuration, _ = factory(prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "http_request_duration_seconds", Help: "Duration of HTTP requests in seconds",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"method", "path", "status"})).(*prometheus.HistogramVec)
	m.HTTPActive, _ = factory(prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "http_active_connections", Help: "Number of active HTTP connections",
	})).(prometheus.Gauge)
	m.DatabaseQuery, _ = factory(prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: "database_query_duration_seconds", Help: "Duration of database queries in seconds",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
	}, []string{"operation", "table"})).(*prometheus.HistogramVec)
	m.CacheHits, _ = factory(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cache_hits_total", Help: "Total number of cache hits",
	}, []string{"cache_name"})).(*prometheus.CounterVec)
	m.CacheMisses, _ = factory(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cache_misses_total", Help: "Total number of cache misses",
	}, []string{"cache_name"})).(*prometheus.CounterVec)
	m.QueueJobs, _ = factory(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "queue_jobs_total", Help: "Total number of queue jobs",
	}, []string{"queue_name", "status"})).(*prometheus.CounterVec)
	m.AdminAlerts, _ = factory(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "admin_alerts_total", Help: "Total number of admin alerts raised",
	}, []string{"type", "level"})).(*prometheus.CounterVec)
	m.AdminAlertsDeduped, _ = factory(prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "admin_alerts_deduplicated_total",
		Help: "Total number of admin alerts suppressed by an open alert with the same dedup key",
	}, []string{"type"})).(*prometheus.CounterVec)
	m.ThrottlerFallbacks, _ = factory(prometheus.NewCounter(prometheus.CounterOpts{
		Name: "throttler_storage_fallback_total",
		Help: "Total number of rate limit checks served by the in-memory storage because Redis was unavailable",
	})).(prometheus.Counter)

	if enabled {
		reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})) //nolint:exhaustruct // defaults
	}

	return m
}

// Enabled reports whether HTTP requests are recorded.
func (m *Metrics) Enabled() bool {
	return m.enabled
}

// Write renders every collector in the Prometheus text format.
func (m *Metrics) Write(w http.ResponseWriter) error {
	families, err := m.registry.Gather()
	if err != nil {
		return fmt.Errorf("metrics: gather: %w", err)
	}

	w.Header().Set("Content-Type", ContentType)

	enc := expfmt.NewEncoder(w, expfmt.NewFormat(expfmt.TypeTextPlain))
	for _, f := range families {
		if err := enc.Encode(f); err != nil {
			return fmt.Errorf("metrics: encode: %w", err)
		}
	}

	return nil
}

// Raised counts an admin alert.
func (m *Metrics) Raised(alertType string, level entity.AlertLevel) {
	m.AdminAlerts.WithLabelValues(alertType, string(level)).Inc()
}

// Deduplicated counts an alert suppressed by an open one.
func (m *Metrics) Deduplicated(alertType string) {
	m.AdminAlertsDeduped.WithLabelValues(alertType).Inc()
}

// Job counts a finished queue job attempt.
func (m *Metrics) Job(queue, status string) {
	m.QueueJobs.WithLabelValues(queue, status).Inc()
}
