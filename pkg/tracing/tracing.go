// Package tracing configures OpenTelemetry: traces and logs exported over OTLP/HTTP.
package tracing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const _defaultEndpoint = "http://localhost:4318"

// Config selects the exporter; Endpoint is the OTLP/HTTP base URL.
type Config struct {
	Enabled     bool
	Endpoint    string
	ServiceName string
}

// Provider holds the installed providers.
type Provider struct {
	// LogHandler exports slog records; nil when tracing is disabled.
	LogHandler slog.Handler
	shutdown   []func(context.Context) error
}

// Enabled reports whether spans are exported.
func (p *Provider) Enabled() bool {
	return p.LogHandler != nil
}

// Setup installs the global tracer provider and propagators when enabled.
func Setup(ctx context.Context, cfg Config) (*Provider, error) {
	p := &Provider{LogHandler: nil, shutdown: nil}
	if !cfg.Enabled {
		return p, nil
	}

	base := strings.TrimSuffix(cfg.Endpoint, "/")
	if base == "" {
		base = _defaultEndpoint
	}

	res := resource.NewSchemaless(attribute.String("service.name", cfg.ServiceName))

	traces, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(base+"/v1/traces"))
	if err != nil {
		return nil, fmt.Errorf("tracing: trace exporter: %w", err)
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traces), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	logs, err := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(base+"/v1/logs"))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("tracing: log exporter: %w", err), tp.Shutdown(ctx))
	}

	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)), sdklog.WithResource(res))

	p.LogHandler = otelslog.NewHandler(cfg.ServiceName, otelslog.WithLoggerProvider(lp))
	p.shutdown = []func(context.Context) error{lp.Shutdown, tp.Shutdown}

	return p, nil
}

// Shutdown flushes and stops the exporters.
func (p *Provider) Shutdown(ctx context.Context) error {
	var errs []error

	for _, fn := range p.shutdown {
		if err := fn(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("tracing: shutdown: %w", err)
	}

	return nil
}
