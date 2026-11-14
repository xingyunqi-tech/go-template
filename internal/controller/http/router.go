// Package http assembles the chi router that serves the nestjs-compatible API.
package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/metrics"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// Options configures NewRouter.
type Options struct {
	APIPrefix      string
	SwaggerEnabled bool
	// Docs is the OpenAPI document served at /docs-json; operations are read from it.
	Docs           []byte
	Validator      *validatorx.Validator
	Translator     *i18n.Translator
	Log            *slog.Logger
	Production     bool
	FrontendDomain string
	Guards         map[Auth]Guard
	Permission     PermissionGuard
	Transactor     Transactor
	TrustProxy     int
	Throttle       ThrottleOptions
	NestHandlers   []byte
	Uploads        UploadOptions
	Auditor        Auditor
	// RawRoutes are operations nestjs excludes from the document; Pattern uses the document's
	// /api prefix and {param} syntax.
	RawRoutes []RawRoute
	// Tracing wraps the handler in an OpenTelemetry server span named after the route.
	Tracing bool
	// ReportError receives the errors answered with a 5xx, for Sentry.
	ReportError func(ctx context.Context, err error)
	// MonitorToken is METRICS_TOKEN, checked by the monitor-token guard.
	MonitorToken string
	// Metrics serves /<MetricsPath> and records the HTTP metrics; nil disables both.
	Metrics     *metrics.Metrics
	MetricsPath string
}

// RawRoute is an undocumented operation writing its own response.
type RawRoute struct {
	ID      string
	Method  string
	Pattern string
	Params  []string
	Handler func(w http.ResponseWriter, r *http.Request) error
}

// Router is the root handler: the global middleware chain, Express-style path matching and the
// operation pipeline in front of a chi mux holding the generated routes and the documentation.
type Router struct {
	http.Handler

	mux chi.Routes
}

// Routes exposes the registered chi routes.
func (r *Router) Routes() chi.Routes {
	return r.mux
}

// NewRouter builds the Router.
func NewRouter(server gen.StrictServerInterface, opts *Options) (*Router, error) {
	errs := &errorWriter{translator: opts.Translator, log: opts.Log, report: opts.ReportError}
	writeErr := func(w http.ResponseWriter, r *http.Request, err error) { errs.write(w, r, err) }
	notFound := func(w http.ResponseWriter, r *http.Request) {
		errs.write(w, r, httperr.New(http.StatusNotFound, "Cannot "+r.Method+" "+r.RequestURI))
	}

	mux := chi.NewRouter()
	mux.Use(middleware.GetHead)
	mux.NotFound(notFound)
	mux.MethodNotAllowed(notFound)

	middlewares := []gen.StrictMiddlewareFunc{transactional(opts.Transactor, Policies()), audited(opts.Auditor, Policies())}
	strict := gen.NewStrictHandlerWithOptions(server, middlewares, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, _ error) {
			errs.write(w, r, httperr.Status(http.StatusBadRequest))
		},
		ResponseErrorHandlerFunc: writeErr,
	})
	gen.HandlerWithOptions(strict, gen.ChiServerOptions{
		BaseURL:          "",
		BaseRouter:       mux,
		Middlewares:      nil,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) { errs.write(w, r, bindError(err)) },
	})

	if opts.SwaggerEnabled {
		mountDocs(mux, opts.Docs)
	}

	extra := mountExtra(mux, opts, errs)

	matcher, err := newPathMatcher(mux, opts.APIPrefix)
	if err != nil {
		return nil, fmt.Errorf("http: collect routes: %w", err)
	}

	pipe, err := newPipeline(mux, opts, errs, extra)
	if err != nil {
		return nil, err
	}

	var handler http.Handler = mux

	handler = pipe.middleware(handler)
	handler = matcher.middleware(handler)
	handler = jsonBody(errs)(handler)
	handler = clientInfo(opts.TrustProxy)(handler)
	handler = secureHeaders(handler)
	handler = accessLog(opts.Log, errs)(handler)
	handler = observe(opts.Metrics)(handler)
	handler = cors(newCORSOptions(opts.Production, opts.FrontendDomain))(handler)
	handler = requestContext(opts.Translator)(handler)

	if opts.Tracing {
		handler = otelhttp.NewHandler(handler, "http.server")
	}

	return &Router{Handler: handler, mux: mux}, nil
}

// mountExtra mounts the undocumented routes and returns their operations.
func mountExtra(mux chi.Router, opts *Options, errs *errorWriter) []*operation {
	var extra []*operation

	for _, rr := range opts.RawRoutes {
		handle := rr.Handler
		mux.Method(rr.Method, rr.Pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := handle(w, r); err != nil {
				errs.write(w, r, err)
			}
		}))

		op := &operation{id: rr.ID, method: rr.Method, pattern: rr.Pattern, pathParams: nil, bodyDTO: "", bearer: false}
		for _, name := range rr.Params {
			op.pathParams = append(op.pathParams, pathParam{name: name, numeric: false})
		}

		extra = append(extra, op)
	}

	if opts.Metrics != nil {
		mountMetrics(mux, opts.MetricsPath, opts.Metrics, errs)
		extra = append(extra, &operation{
			id: "getMetrics", method: http.MethodGet, pattern: "/" + opts.MetricsPath, pathParams: nil, bodyDTO: "", bearer: false,
		})
	}

	return extra
}

func newPipeline(mux chi.Routes, opts *Options, errs *errorWriter, extra []*operation) (*pipeline, error) {
	ops, err := parseOperations(opts.Docs)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}

	for _, op := range extra {
		ops[operationKey(op.method, op.pattern)] = op
	}

	var registered []*operation

	err = chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if op, ok := ops[operationKey(method, route)]; ok && op.method == method {
			registered = append(registered, op)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("http: collect routes: %w", err)
	}

	throttle, err := newThrottler(opts.Throttle, opts.NestHandlers, opts.TrustProxy)
	if err != nil {
		return nil, err
	}

	guards := make(map[Auth]Guard, len(opts.Guards)+1)
	for k, g := range opts.Guards {
		guards[k] = g
	}

	guards[AuthMonitor] = MonitorGuard(opts.MonitorToken, opts.Production)

	pipe := &pipeline{
		throttle:   throttle,
		ops:        ops,
		policies:   Policies(),
		guards:     guards,
		uploads:    opts.Uploads,
		auditing:   opts.Auditor != nil,
		permission: opts.Permission,
		validator:  opts.Validator,
		errs:       errs,
	}

	if err := pipe.check(registered); err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}

	return pipe, nil
}

// bindError maps generated parameter binding failures: a path id is ParseIdPipe's 400 invalidId,
// anything else a plain 400.
func bindError(err error) error {
	var invalid *gen.InvalidParamFormatError
	if errors.As(err, &invalid) && invalid.ParamName == "id" {
		return httperr.InvalidID()
	}

	return httperr.Status(http.StatusBadRequest)
}
