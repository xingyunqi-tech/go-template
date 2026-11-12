// Package http assembles the chi router that serves the nestjs-compatible API.
package http

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
)

// Options configures NewRouter.
type Options struct {
	APIPrefix      string
	SwaggerEnabled bool
	Docs           []byte
}

// Router is the root handler: Express-style path matching in front of a chi mux holding the
// generated routes and the documentation endpoints.
type Router struct {
	http.Handler

	mux chi.Routes
}

// Routes exposes the registered chi routes.
func (r *Router) Routes() chi.Routes {
	return r.mux
}

// NewRouter builds the Router.
func NewRouter(server gen.StrictServerInterface, opts Options) (*Router, error) {
	mux := chi.NewRouter()
	mux.Use(middleware.GetHead)
	mux.NotFound(notFound)
	mux.MethodNotAllowed(notFound)

	strict := gen.NewStrictHandler(server, nil)
	gen.HandlerWithOptions(strict, gen.ChiServerOptions{BaseRouter: mux})

	if opts.SwaggerEnabled {
		mountDocs(mux, opts.Docs)
	}

	matcher, err := newPathMatcher(mux, opts.APIPrefix)
	if err != nil {
		return nil, fmt.Errorf("http: collect routes: %w", err)
	}

	return &Router{Handler: matcher.middleware(mux), mux: mux}, nil
}

func notFound(w http.ResponseWriter, r *http.Request) {
	_ = response.WriteError(w, http.StatusNotFound, "Cannot "+r.Method+" "+r.RequestURI, nil)
}
