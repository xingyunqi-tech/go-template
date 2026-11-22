package http_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
)

func TestServerMetricsCarryTheRouteTemplate(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	h := newHarness(t, true, func(o *httpapi.Options) { o.MeterProvider = provider })

	h.do(http.MethodGet, "/api/v1/items/019a8f3c-6b1e-7d2a-9c4f-1e2d3c4b5a69", "")
	h.do(http.MethodGet, "/health/live", "")
	h.do(http.MethodGet, "/api/v1/unknown", "")

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))

	routes := map[string]int64{}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}

			hist, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok)

			for _, dp := range hist.DataPoints {
				route, _ := dp.Attributes.Value(attribute.Key("http.route"))
				method, _ := dp.Attributes.Value(attribute.Key("http.request.method"))
				status, _ := dp.Attributes.Value(attribute.Key("http.response.status_code"))

				assert.Equal(t, "GET", method.AsString())
				routes[route.AsString()+" "+status.String()] += int64(dp.Count) //nolint:gosec // a handful of requests
			}
		}
	}

	assert.Equal(t, map[string]int64{
		"/api/v1/items/{id} 200": 1,
		"/health/live 200":       1,
		" 404":                   1,
	}, routes)
}
