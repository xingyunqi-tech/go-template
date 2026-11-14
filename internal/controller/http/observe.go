package http

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xingyunqi-tech/go-template/internal/metrics"
)

const (
	_clientClosedRequest = 499
	_noCache             = "no-cache, no-store, must-revalidate"
)

// exchange collects what inner middleware learns about a request for the outer logging and
// metrics middleware.
type exchange struct {
	route  string
	err    error
	userID string
}

type exchangeKey struct{}

func withExchange(ctx context.Context) (context.Context, *exchange) {
	ex := &exchange{route: "", err: nil, userID: ""}

	return context.WithValue(ctx, exchangeKey{}, ex), ex
}

func exchangeOf(ctx context.Context) *exchange {
	ex, _ := ctx.Value(exchangeKey{}).(*exchange)

	return ex
}

func setRoute(ctx context.Context, route string) {
	if ex := exchangeOf(ctx); ex != nil {
		ex.route = route
	}
}

func setUserID(ctx context.Context, id string) {
	if ex := exchangeOf(ctx); ex != nil {
		ex.userID = id
	}
}

func setError(ctx context.Context, err error) {
	if ex := exchangeOf(ctx); ex != nil {
		ex.err = err
	}
}

// responseWriter records the status and whether writing to the client failed.
type responseWriter struct {
	http.ResponseWriter

	status  int
	failed  bool
	written int64
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}

	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}

	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)

	if err != nil {
		w.failed = true

		return n, err //nolint:wrapcheck // the net/http error is passed through
	}

	return n, nil
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// finalStatus is the status the client received, or 499 when it went away before the response
// was written.
func (w *responseWriter) finalStatus(r *http.Request) int {
	if w.failed || (w.status == 0 && r.Context().Err() != nil) {
		return _clientClosedRequest
	}

	if w.status == 0 {
		return http.StatusOK
	}

	return w.status
}

// observe records the nestjs MetricsMiddleware samples when the response ends.
func observe(m *metrics.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, ex := withExchange(r.Context())
			rw, ok := w.(*responseWriter)

			if !ok {
				rw = &responseWriter{ResponseWriter: w, status: 0, failed: false, written: 0}
			}

			defer func() { //nolint:contextcheck // the span belongs to the outer request context
				if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
					span.SetName(r.Method + " " + routeOrUnmatched(ex.route))
					span.SetAttributes(attribute.String("http.route", routeOrUnmatched(ex.route)))
				}
			}()

			if m == nil || !m.Enabled() {
				next.ServeHTTP(rw, r.WithContext(ctx))

				return
			}

			start := time.Now()

			m.HTTPActive.Inc()
			defer func() {
				m.HTTPActive.Dec()

				route := routeOrUnmatched(ex.route)
				status := strconv.Itoa(rw.finalStatus(r))
				m.HTTPRequests.WithLabelValues(r.Method, route, status).Inc()
				m.HTTPDuration.WithLabelValues(r.Method, route, status).Observe(time.Since(start).Seconds())
			}()

			next.ServeHTTP(rw, r.WithContext(ctx))
		})
	}
}

func routeOrUnmatched(route string) string {
	if route == "" {
		return metrics.Unmatched
	}

	return route
}

// noCacheWriter adds the terminus Cache-Control header to successful responses.
type noCacheWriter struct {
	http.ResponseWriter

	started bool
}

func (w *noCacheWriter) WriteHeader(status int) {
	if !w.started && status >= http.StatusOK && status < http.StatusMultipleChoices {
		w.Header().Set("Cache-Control", _noCache)
	}

	w.started = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *noCacheWriter) Write(b []byte) (int, error) {
	if !w.started {
		w.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(b) //nolint:wrapcheck // pass-through
}

func (w *noCacheWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func mountMetrics(r interface {
	Get(pattern string, h http.HandlerFunc)
}, path string, m *metrics.Metrics, errs *errorWriter,
) {
	r.Get("/"+path, func(w http.ResponseWriter, req *http.Request) {
		if err := m.Write(w); err != nil {
			errs.write(w, req, err)
		}
	})
}
