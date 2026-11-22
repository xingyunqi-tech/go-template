package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/config"
)

const _metricsReadHeaderTimeout = 5 * time.Second

// metricsMux serves GET /metrics, behind METRICS_TOKEN when it is set.
func metricsMux(token string, metrics http.Handler) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		if token != "" {
			got, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)

				return
			}
		}

		metrics.ServeHTTP(w, r)
	})

	return mux
}

// serveMetrics listens on port; stop shuts the server down.
func serveMetrics(ctx context.Context, cfg *config.Config, port int, metrics http.Handler, log *slog.Logger) (stop func(), err error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort("", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("app: metrics listener: %w", err)
	}

	srv := &http.Server{Handler: metricsMux(cfg.Metrics.Token.Value(), metrics), ReadHeaderTimeout: _metricsReadHeaderTimeout} //nolint:exhaustruct // defaults

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.ErrorContext(ctx, "metrics server failed", "error", err.Error())
		}
	}()

	log.InfoContext(ctx, "metrics listening", "addr", listener.Addr().String())

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.App.ShutdownTimeout)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	}, nil
}
