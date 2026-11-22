// Package telemetry configures OpenTelemetry from the standard OTEL_* variables: traces and logs
// through autoexport, metrics pulled by Prometheus or pushed over OTLP.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Metrics exporters accepted by Options.MetricsExporter.
const (
	MetricsPrometheus = "prometheus"
	MetricsOTLP       = "otlp"
	MetricsNone       = "none"
)

// Options selects what Setup installs.
type Options struct {
	// Disabled is OTEL_SDK_DISABLED: nothing is installed.
	Disabled bool
	// MetricsExporter is OTEL_METRICS_EXPORTER.
	MetricsExporter string
}

// Provider holds the installed providers.
type Provider struct {
	// LogHandler exports slog records; nil when OTEL_LOGS_EXPORTER is none.
	LogHandler slog.Handler
	// Registry holds the Prometheus metrics; nil unless the exporter is prometheus.
	Registry *prometheus.Registry
	shutdown []func(context.Context) error
}

// Setup installs the global propagators and the tracer, meter and logger providers.
func Setup(ctx context.Context, opts Options) (*Provider, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	p := &Provider{LogHandler: nil, Registry: nil, shutdown: nil}
	if opts.Disabled {
		return p, nil
	}

	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithTelemetrySDK())
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource: %w", err)
	}

	for _, install := range []func(context.Context, *resource.Resource) error{
		p.installTraces,
		func(ctx context.Context, res *resource.Resource) error {
			return p.installMetrics(ctx, res, opts.MetricsExporter)
		},
		p.installLogs,
	} {
		if err := install(ctx, res); err != nil {
			return nil, errors.Join(err, p.Shutdown(ctx))
		}
	}

	return p, nil
}

func (p *Provider) installTraces(ctx context.Context, res *resource.Resource) error {
	exporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return fmt.Errorf("telemetry: span exporter: %w", err)
	}

	if autoexport.IsNoneSpanExporter(exporter) {
		return nil
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	p.shutdown = append(p.shutdown, tp.Shutdown)

	return nil
}

func (p *Provider) installMetrics(ctx context.Context, res *resource.Resource, exporter string) error {
	var reader sdkmetric.Reader

	switch exporter {
	case MetricsNone:
		return nil
	case MetricsPrometheus:
		registry := prometheus.NewRegistry()
		registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})) //nolint:exhaustruct // defaults

		promExporter, err := otelprom.New(otelprom.WithRegisterer(registry))
		if err != nil {
			return fmt.Errorf("telemetry: prometheus exporter: %w", err)
		}

		p.Registry, reader = registry, promExporter
	default:
		autoReader, err := autoexport.NewMetricReader(ctx)
		if err != nil {
			return fmt.Errorf("telemetry: metric reader: %w", err)
		}

		if autoexport.IsNoneMetricReader(autoReader) {
			return nil
		}

		reader = autoReader
	}

	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithResource(res))
	otel.SetMeterProvider(mp)
	p.shutdown = append(p.shutdown, mp.Shutdown)

	return nil
}

func (p *Provider) installLogs(ctx context.Context, res *resource.Resource) error {
	exporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return fmt.Errorf("telemetry: log exporter: %w", err)
	}

	if autoexport.IsNoneLogExporter(exporter) {
		return nil
	}

	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)), sdklog.WithResource(res))
	p.LogHandler = otelslog.NewHandler("github.com/xingyunqi-tech/go-template", otelslog.WithLoggerProvider(lp))
	p.shutdown = append(p.shutdown, lp.Shutdown)

	return nil
}

// Shutdown flushes and stops the providers.
func (p *Provider) Shutdown(ctx context.Context) error {
	var errs []error

	for _, fn := range p.shutdown {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("telemetry: shutdown: %w", err)
	}

	return nil
}
