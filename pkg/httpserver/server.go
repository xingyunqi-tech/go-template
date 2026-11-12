// Package httpserver runs a net/http server with graceful shutdown.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

const (
	_readHeaderTimeout = 10 * time.Second
	_readTimeout       = 60 * time.Second
	_writeTimeout      = 60 * time.Second
	_idleTimeout       = 120 * time.Second
)

// Server wraps http.Server.
type Server struct {
	srv *http.Server
}

// New returns a server for handler on addr.
func New(addr string, handler http.Handler) *Server {
	return &Server{srv: &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: _readHeaderTimeout,
		ReadTimeout:       _readTimeout,
		WriteTimeout:      _writeTimeout,
		IdleTimeout:       _idleTimeout,
	}}
}

// Serve accepts connections on ln until Shutdown; it returns nil after a graceful shutdown.
func (s *Server) Serve(ln net.Listener) error {
	if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("httpserver: %w", err)
	}

	return nil
}

// Shutdown stops accepting connections and waits for in-flight requests until ctx is done.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("httpserver: shutdown: %w", err)
	}

	return nil
}
