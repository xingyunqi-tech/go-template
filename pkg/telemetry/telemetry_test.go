package telemetry_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"

	"github.com/xingyunqi-tech/go-template/pkg/telemetry"
)

func TestPrometheusModeExposesARegistry(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	p, err := telemetry.Setup(t.Context(), telemetry.Options{Disabled: false, MetricsExporter: telemetry.MetricsPrometheus})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, p.Shutdown(t.Context())) })

	require.NotNil(t, p.Registry)
	assert.Nil(t, p.LogHandler)

	counter, err := otel.Meter("test").Int64Counter("demo.requests")
	require.NoError(t, err)
	counter.Add(t.Context(), 1)

	families, err := p.Registry.Gather()
	require.NoError(t, err)

	var names []string
	for _, f := range families {
		names = append(names, f.GetName())
	}

	assert.Contains(t, names, "demo_requests_total")
	assert.Contains(t, names, "go_goroutines")
	assert.Contains(t, names, "target_info")
}

func TestDisabledInstallsNothing(t *testing.T) {
	t.Parallel()

	p, err := telemetry.Setup(t.Context(), telemetry.Options{Disabled: true, MetricsExporter: telemetry.MetricsPrometheus})
	require.NoError(t, err)
	assert.Nil(t, p.Registry)
	assert.Nil(t, p.LogHandler)
	require.NoError(t, p.Shutdown(t.Context()))
}

func TestNoneModeHasNoRegistry(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_LOGS_EXPORTER", "console")

	p, err := telemetry.Setup(t.Context(), telemetry.Options{Disabled: false, MetricsExporter: telemetry.MetricsNone})
	require.NoError(t, err)
	t.Cleanup(func() { _ = p.Shutdown(t.Context()) })

	assert.Nil(t, p.Registry)
	assert.NotNil(t, p.LogHandler)
}
