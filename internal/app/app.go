// Package app wires configuration, infrastructure and controllers, and runs the process roles.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"

	"github.com/xingyunqi-tech/go-template/internal/config"
	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	v1 "github.com/xingyunqi-tech/go-template/internal/controller/http/v1"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/httpserver"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// NewHandler builds the HTTP handler of the API.
func NewHandler(cfg *config.Config, log *slog.Logger) (http.Handler, error) {
	translator, err := i18n.New(cfg.App.FallbackLanguage)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	validator, err := validatorx.New(openapi.Validation)
	if err != nil {
		return nil, fmt.Errorf("app: validation.json: %w", err)
	}

	handler, err := httpapi.NewRouter(v1.New(cfg.App.Name), &httpapi.Options{
		APIPrefix:      cfg.App.APIPrefix,
		SwaggerEnabled: cfg.App.Docs(),
		Docs:           openapi.Baseline,
		Validator:      validator,
		Translator:     translator,
		Log:            log,
		Production:     cfg.App.IsProduction(),
		FrontendDomain: cfg.App.FrontendDomain,
		Guards:         nil,
		Permission:     nil,
	})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	return handler, nil
}

// Serve runs the API server until ctx is canceled, then shuts down gracefully.
func Serve(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	handler, err := NewHandler(cfg, log)
	if err != nil {
		return err
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", ":"+strconv.Itoa(cfg.App.Port))
	if err != nil {
		return fmt.Errorf("app: listen: %w", err)
	}

	srv := httpserver.New(ln.Addr().String(), handler)
	serveErr := make(chan error, 1)

	go func() { serveErr <- srv.Serve(ln) }()

	log.InfoContext(ctx, "http server started", "addr", ln.Addr().String(), "env", cfg.App.Env)

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	log.InfoContext(ctx, "shutting down", "timeout", cfg.App.ShutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.App.ShutdownTimeout)
	defer cancel()

	return errors.Join(srv.Shutdown(shutdownCtx), <-serveErr)
}
