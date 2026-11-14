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
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/internal/usecase/permission"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/httpserver"
	"github.com/xingyunqi-tech/go-template/pkg/password"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

func authConfig(a *config.Auth) auth.Config {
	return auth.Config{
		Access:       auth.Secret{Key: a.Secret.Value(), ExpiresMillis: a.Expires.Millis},
		Refresh:      auth.Secret{Key: a.RefreshSecret.Value(), ExpiresMillis: a.RefreshExpires.Millis},
		Forgot:       auth.Secret{Key: a.ForgotSecret.Value(), ExpiresMillis: a.ForgotExpires.Millis},
		ConfirmEmail: auth.Secret{Key: a.ConfirmEmailSecret.Value(), ExpiresMillis: a.ConfirmEmailExpires.Millis},
	}
}

// NewHandler builds the HTTP handler of the API on top of infra.
func NewHandler(cfg *config.Config, log *slog.Logger, infra *Infra) (http.Handler, error) {
	translator, err := i18n.New(cfg.App.FallbackLanguage)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	validator, err := validatorx.New(openapi.Validation)
	if err != nil {
		return nil, fmt.Errorf("app: validation.json: %w", err)
	}

	hasher, err := password.New(cfg.Auth.PasswordHasher, cfg.Auth.HashConcurrency)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	db := postgres.NewDB(infra.Pool, acquireTimeout(cfg), log)
	cache := redis.NewCache(infra.Redis, log)
	userRepo := postgres.NewUsers(db)
	sessionRepo := postgres.NewSessions(db)
	sessions := session.New(sessionRepo, cache, db, log)
	users := user.New(userRepo, postgres.NewFiles(db), sessions, hasher, logCleanup{log: log}, log)
	authService := auth.New(&auth.Deps{
		Config: authConfig(&cfg.Auth), Users: users, UserRepo: userRepo, Sessions: sessions, SessRepo: sessionRepo,
		Cache: cache, Tx: db, Hasher: hasher, Mailer: logMailer{log: log}, SMS: sms.NewCodes(cache), Log: log,
	})

	server := v1.New(v1.Deps{
		AppName: cfg.App.Name, Auth: authService, Users: users,
		URLs: view.FileURLs{BackendDomain: cfg.App.BackendDomain, APIPrefix: cfg.App.APIPrefix},
	})

	handler, err := httpapi.NewRouter(server, &httpapi.Options{
		APIPrefix:      cfg.App.APIPrefix,
		SwaggerEnabled: cfg.App.Docs(),
		Docs:           openapi.Baseline,
		Validator:      validator,
		Translator:     translator,
		Log:            log,
		Production:     cfg.App.IsProduction(),
		FrontendDomain: cfg.App.FrontendDomain,
		Guards:         httpapi.AuthGuards(authService),
		Permission:     httpapi.RequirePermissions(permission.New(postgres.NewPermissions(db), cache)),
		Transactor:     transactor{db: db},
		TrustProxy:     cfg.App.TrustProxy,
	})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	return handler, nil
}

// Serve runs the API server until ctx is canceled, then shuts down gracefully.
func Serve(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	infra, err := OpenInfra(ctx, cfg, cfg.Database.MaxConnections)
	if err != nil {
		return err
	}
	defer infra.Close()

	if cfg.Database.SchemaOwner == config.SchemaOwnerExternal {
		if err := CheckSchema(ctx, infra.Pool); err != nil {
			return err
		}
	}

	handler, err := NewHandler(cfg, log, infra)
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
