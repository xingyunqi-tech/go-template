package http

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
)

// ValidationDocument is the fully inlined OpenAPI document of the requests, produced by
// make openapi.
//
//go:embed validation.json
var ValidationDocument []byte

const (
	_scalarVersion   = "1.80.0"
	_readyRetryAfter = "5"
	_docsCSP         = "default-src 'self'; script-src 'self' https://cdn.jsdelivr.net 'unsafe-inline'; " +
		"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; img-src 'self' data: https:; font-src 'self' data: https:; connect-src 'self'"
)

// _docsPage loads Scalar from the CDN and points it at /openapi.json.
const _docsPage = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>API</title>
</head>
<body>
<script id="api-reference" data-url="/openapi.json"></script>
<script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@` + _scalarVersion + `"></script>
</body>
</html>
`

// mountOps registers the routes outside /api/v1: health, metrics and documentation.
func mountOps(mux chi.Router, opts *Options, errs *errorWriter) {
	mux.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": health.StatusOK})
	})
	mux.Get("/health/ready", readyHandler(opts.Readiness))

	if opts.Metrics != nil {
		mux.Get("/metrics", metricsHandler(opts.Metrics, opts.MetricsToken, errs))
	}

	if opts.DocsEnabled {
		mux.Get("/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(opts.Document)
		})
		mux.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", _docsCSP)
			_, _ = w.Write([]byte(_docsPage))
		})
	}
}

func readyHandler(readiness Readiness) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report := readiness.Ready(r.Context())

		status, body := http.StatusOK, map[string]any{"status": health.StatusOK, "checks": report.Checks}
		if !report.Ready {
			status, body["status"] = http.StatusServiceUnavailable, health.StatusError

			w.Header().Set("Retry-After", _readyRetryAfter)
		}

		writeJSON(w, status, body)
	}
}

// metricsHandler serves metrics behind token when it is set.
func metricsHandler(metrics http.Handler, token string, errs *errorWriter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			metrics.ServeHTTP(w, r)

			return
		}

		got, present := bearer(r)
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1 {
			metrics.ServeHTTP(w, r)

			return
		}

		var err error = entity.New(entity.KeyUnauthorized)
		if present {
			err = entity.Wrap(entity.KeyUnauthorized, httperr.ErrInvalidToken)
		}

		errs.write(w, r, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
