package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/pkg/tracing"
)

const _sentryFlushTimeout = 2 * time.Second

// Observability is the process-wide tracing and Sentry setup.
type Observability struct {
	tracing *tracing.Provider
	sentry  bool
}

// SetupObservability installs OpenTelemetry when OTEL_ENABLED and Sentry when SENTRY_DSN is set.
func SetupObservability(ctx context.Context, cfg *config.Config, release string) (*Observability, error) {
	provider, err := tracing.Setup(ctx, tracing.Config{
		Enabled: cfg.Observability.OTelEnabled.Or(false), Endpoint: cfg.Observability.OTelEndpoint, ServiceName: cfg.App.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	o := &Observability{tracing: provider, sentry: false}

	if dsn := cfg.Observability.SentryDSN.Value(); dsn != "" {
		err := sentry.Init(sentry.ClientOptions{ //nolint:exhaustruct // SDK defaults
			Dsn: dsn, Environment: cfg.App.Env, Release: release,
		})
		if err != nil {
			return nil, errors.Join(fmt.Errorf("app: sentry: %w", err), provider.Shutdown(ctx))
		}

		o.sentry = true
	}

	return o, nil
}

// Logger also exports the records of base through OpenTelemetry when it is enabled.
func (o *Observability) Logger(base *slog.Logger) *slog.Logger {
	if o.tracing.LogHandler == nil {
		return base
	}

	return slog.New(slog.NewMultiHandler(base.Handler(), o.tracing.LogHandler))
}

// Shutdown flushes Sentry and the OpenTelemetry exporters.
func (o *Observability) Shutdown(ctx context.Context) error {
	if o.sentry {
		sentry.Flush(_sentryFlushTimeout)
	}

	return o.tracing.Shutdown(ctx) //nolint:wrapcheck // wrapped by tracing
}

// reportError sends err to Sentry when SENTRY_DSN is set.
func reportError(cfg *config.Config) func(ctx context.Context, err error) {
	if cfg.Observability.SentryDSN.Value() == "" {
		return nil
	}

	return func(ctx context.Context, err error) {
		hub := sentry.GetHubFromContext(ctx)
		if hub == nil {
			hub = sentry.CurrentHub()
		}

		hub.CaptureException(err)
	}
}

// jobErrors logs failed and panicking jobs and reports them to Sentry.
type jobErrors struct {
	log    *slog.Logger
	report func(ctx context.Context, err error)
}

func (h jobErrors) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	h.log.ErrorContext(ctx, "job failed", "job_id", job.ID, "kind", job.Kind, "queue", job.Queue, "attempt", job.Attempt, "error", err.Error())

	if h.report != nil {
		h.report(ctx, err)
	}

	return nil
}

func (h jobErrors) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	err := fmt.Errorf("job %d (%s) panicked: %v", job.ID, job.Kind, panicVal) //nolint:err113 // panic value

	h.log.ErrorContext(ctx, "job panicked", "error", err.Error(), "trace", trace)

	if h.report != nil {
		h.report(ctx, err)
	}

	return nil
}
