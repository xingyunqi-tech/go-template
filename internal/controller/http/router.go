// Package http assembles the chi router that serves the nestjs-compatible API.
package http

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
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
	errs := &errorWriter{translator: opts.Translator, log: opts.Log}
	writeErr := func(w http.ResponseWriter, r *http.Request, err error) { errs.write(w, r, err) }
	notFound := func(w http.ResponseWriter, r *http.Request) {
		errs.write(w, r, httperr.New(http.StatusNotFound, "Cannot "+r.Method+" "+r.RequestURI))
	}

	mux := chi.NewRouter()
	mux.Use(middleware.GetHead)
	mux.NotFound(notFound)
	mux.MethodNotAllowed(notFound)

	middlewares := []gen.StrictMiddlewareFunc{transactional(opts.Transactor, Policies())}
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

	matcher, err := newPathMatcher(mux, opts.APIPrefix)
	if err != nil {
		return nil, fmt.Errorf("http: collect routes: %w", err)
	}

	pipe, err := newPipeline(mux, opts, errs)
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
	handler = cors(newCORSOptions(opts.Production, opts.FrontendDomain))(handler)
	handler = requestContext(opts.Translator)(handler)

	return &Router{Handler: handler, mux: mux}, nil
}

func newPipeline(mux chi.Routes, opts *Options, errs *errorWriter) (*pipeline, error) {
	ops, err := parseOperations(opts.Docs)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
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

	pipe := &pipeline{
		ops:        ops,
		policies:   Policies(),
		guards:     opts.Guards,
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
