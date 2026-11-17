package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/config"
	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	"github.com/xingyunqi-tech/go-template/internal/metrics"
)

const _metricsReadHeaderTimeout = 5 * time.Second

// workerMetricsHandler serves GET /<METRICS_PATH> behind the METRICS_TOKEN guard.
func workerMetricsHandler(cfg *config.Config, m *metrics.Metrics) http.Handler {
	guard := httpapi.MonitorGuard(cfg.Metrics.Token.Value(), cfg.App.IsProduction())
	mux := http.NewServeMux()

	mux.HandleFunc("GET /"+cfg.Metrics.Path, func(w http.ResponseWriter, r *http.Request) {
		if _, err := guard(r); err != nil {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":403,"message":"Forbidden resource","data":null}`))

			return
		}

		if err := m.Write(w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	return mux
}

// serveWorkerMetrics listens on WORKER_METRICS_PORT; stop shuts the server down and is a no-op
// when the port is 0.
func serveWorkerMetrics(ctx context.Context, cfg *config.Config, m *metrics.Metrics, log *slog.Logger) (stop func(), err error) {
	if cfg.Metrics.WorkerPort == 0 {
		return func() {}, nil
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort("", strconv.Itoa(cfg.Metrics.WorkerPort)))
	if err != nil {
		return nil, fmt.Errorf("app: worker metrics: %w", err)
	}

	srv := &http.Server{Handler: workerMetricsHandler(cfg, m), ReadHeaderTimeout: _metricsReadHeaderTimeout}

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.ErrorContext(ctx, "worker metrics server failed", "error", err.Error())
		}
	}()

	log.InfoContext(ctx, "worker metrics listening", "addr", listener.Addr().String(), "path", "/"+cfg.Metrics.Path)

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.App.ShutdownTimeout)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	}, nil
}
