// Package http assembles the chi router: the global middleware chain, the operations routes,
// and the /api/v1 pipeline of authentication, rate limits, validation, idempotency and
// transactions in front of the generated handlers.
package http

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
	"uuid"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
	"github.com/xingyunqi-tech/go-template/pkg/clientip"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

// APIPrefix is the path prefix of the business routes.
const APIPrefix = "/api/v1"

// Verifier checks access tokens.
type Verifier interface {
	Verify(ctx context.Context, token string) (*jwtx.Claims, error)
}

// Revocations reports sessions revoked before their access tokens expire.
type Revocations interface {
	IsRevoked(ctx context.Context, sid uuid.UUID) (bool, error)
}

// Authorizer reports whether one of roles grants a permission code.
type Authorizer interface {
	Granted(roles []string, permission string) bool
}

// Tx is an open request transaction.
type Tx interface {
	// Commit reports false when PostgreSQL rolled the failed transaction back instead.
	Commit(ctx context.Context) (bool, error)
	Rollback(ctx context.Context)
}

// Transactor opens request transactions.
type Transactor interface {
	Begin(ctx context.Context) (context.Context, Tx, error)
}

// Auditor writes audit log entries.
type Auditor interface {
	Log(ctx context.Context, l *repo.AuditLog) error
}

// Readiness reports whether the service can take traffic.
type Readiness interface {
	Ready(ctx context.Context) health.Report
}

// Options configures NewRouter.
type Options struct {
	Log        *slog.Logger
	Translator *i18n.Translator
	// ServiceName is OTEL_SERVICE_NAME, the prefix of the private rate limit rules.
	ServiceName string
	// ValidationDocument is the inlined OpenAPI document the requests are validated against.
	ValidationDocument []byte
	// Document is served at /openapi.json when DocsEnabled.
	Document    []byte
	DocsEnabled bool
	// CORSOrigins is the origin whitelist; AnyOrigin allows every origin instead.
	CORSOrigins    []string
	AnyOrigin      bool
	BodyLimit      int64
	TrustedProxies clientip.Proxies
	Policies       map[string]Policy
	// Register mounts the generated handlers; writeError writes the errors they return.
	Register func(r chi.Router, writeError func(http.ResponseWriter, *http.Request, error))

	Verifier    Verifier
	Revocations Revocations
	Authorizer  Authorizer
	Limiter     *ratelimit.Limiter
	RateLimits  bool
	Idempotency repo.IdempotencyStore
	// IdempotencyClaimTTL bounds an in-progress key left by a stopped process; 0 is one minute.
	IdempotencyClaimTTL time.Duration
	Transactor          Transactor
	Auditor             Auditor
	Readiness           Readiness
	// JWKS is served at /.well-known/jwks.json by the issuing service; nil leaves the route out.
	JWKS []byte
	// Metrics serves /metrics; nil leaves the route out. MetricsToken guards it when set.
	Metrics      http.Handler
	MetricsToken string
	// ReportError receives the errors answered with a 5xx, for Sentry.
	ReportError func(ctx context.Context, err error)
	// Now is the clock of the reauthentication check; nil is time.Now.
	Now func() time.Time
	// MeterProvider records the HTTP server metrics; nil is the global provider.
	MeterProvider metric.MeterProvider
}

// Router is the root handler.
type Router struct {
	http.Handler

	mux *chi.Mux
	ops *operations
}

// Routes exposes the registered chi routes.
func (r *Router) Routes() chi.Routes {
	return r.mux
}

// OperationIDs lists the operationIds the router serves.
func (r *Router) OperationIDs() []string {
	return r.ops.ids()
}

// NewRouter builds the Router.
func NewRouter(ctx context.Context, opts *Options) (*Router, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}

	if opts.IdempotencyClaimTTL <= 0 {
		opts.IdempotencyClaimTTL = _defaultClaimTTL
	}

	ops, err := loadOperations(ctx, opts.ValidationDocument, opts.Policies)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}

	errs := &errorWriter{translator: opts.Translator, log: opts.Log, report: opts.ReportError}

	mux := chi.NewRouter()
	mountOps(mux, opts, errs)

	if opts.Register != nil {
		opts.Register(mux, errs.write)
	}

	if err := ops.checkRegistered(mux); err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}

	p := &pipeline{opts: opts, ops: ops, mux: mux, errs: errs}

	var handler http.Handler = p

	handler = withLanguage(opts.Translator)(handler)
	handler = cors(opts.CORSOrigins, opts.AnyOrigin)(handler)
	handler = secureHeaders(handler)
	handler = accessLog(opts.Log, errs)(handler)
	handler = requestID(handler)
	handler = capturePattern(handler)

	otelOpts := []otelhttp.Option{otelhttp.WithSpanNameFormatter(spanName)}
	if opts.MeterProvider != nil {
		otelOpts = append(otelOpts, otelhttp.WithMeterProvider(opts.MeterProvider))
	}

	handler = otelhttp.NewHandler(handler, "http.server", otelOpts...)

	return &Router{Handler: handler, mux: mux, ops: ops}, nil
}

func spanName(_ string, r *http.Request) string {
	if r.Pattern != "" {
		return r.Pattern
	}

	return r.Method
}

// notFound is the error of a path no route matches.
func notFound() error {
	return entity.New(entity.KeyNotFound)
}

// errorWriter writes and logs error responses.
type errorWriter struct {
	translator *i18n.Translator
	log        *slog.Logger
	report     func(ctx context.Context, err error)
}

func (e *errorWriter) translate(ctx context.Context, id string, data map[string]any) string {
	msg, ok := e.translator.Message(i18n.Language(ctx), id, data)
	if !ok {
		return id
	}

	return msg
}

func (e *errorWriter) write(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	setError(ctx, err)

	status, writeErr := httperr.Write(ctx, w, err, e.translate)

	if status >= http.StatusInternalServerError && e.report != nil {
		e.report(ctx, err)
	}

	if writeErr != nil {
		e.log.DebugContext(ctx, "write error response", "error", writeErr.Error())
	}
}
