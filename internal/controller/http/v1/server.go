// Package v1 implements gen.StrictServerInterface.
package v1

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
)

var _ gen.StrictServerInterface = (*Server)(nil)

// Server implements the generated strict server interface.
type Server struct {
	notes usecase.Notes
	log   *slog.Logger
}

// Deps are the Server dependencies.
type Deps struct {
	Notes usecase.Notes
	Log   *slog.Logger
}

// New returns a Server.
func New(d *Deps) *Server {
	return &Server{notes: d.Notes, log: d.Log}
}

// Register mounts the generated strict handlers of s; parameter binding errors are 400s.
func Register(s gen.StrictServerInterface) func(chi.Router, func(http.ResponseWriter, *http.Request, error)) {
	return func(r chi.Router, writeError func(http.ResponseWriter, *http.Request, error)) {
		badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
			writeError(w, r, entity.Wrap(entity.KeyBadRequest, err))
		}

		strict := gen.NewStrictHandlerWithOptions(s, nil, gen.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  badRequest,
			ResponseErrorHandlerFunc: writeError,
		})
		gen.HandlerWithOptions(strict, gen.ChiServerOptions{ //nolint:exhaustruct // defaults
			BaseRouter:       r,
			ErrorHandlerFunc: badRequest,
		})
	}
}
