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
	"time"

	"github.com/xingyunqi-tech/go-template/internal/config"
	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	v1 "github.com/xingyunqi-tech/go-template/internal/controller/http/v1"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/audit"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
	"github.com/xingyunqi-tech/go-template/internal/usecase/note"
	// modulegen:imports
	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/httpserver"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

const _claimMargin = 30 * time.Second

// Handler is the API handler.
type Handler struct {
	http.Handler

	health *health.Service
}

// Drain fails the readiness probe while the server shuts down.
func (h *Handler) Drain() {
	h.health.Drain()
}

// NewHandler builds the HTTP handler of the API on top of infra; metrics serves /metrics when
// it is not on its own port.
func NewHandler(ctx context.Context, cfg *config.Config, log *slog.Logger, infra *Infra, metrics http.Handler) (*Handler, error) {
	translator, err := i18n.New(cfg.App.DefaultLocale)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	keys, err := NewKeys(ctx, &cfg.Auth, log)
	if err != nil {
		return nil, err
	}

	db := postgres.NewDB(infra.Pool, _acquireTimeout, log)
	healthService := newHealth(db, infra)
	limiter := ratelimit.New(infra.Redis)
	revocations := redis.NewRevocations(infra.Redis)

	auditService := audit.New(postgres.NewAudits(db))

	server, jwks, err := newServer(&identityDeps{
		cfg: cfg, log: log, db: db, infra: infra, keys: keys, translator: translator, limiter: limiter, revoker: revocations,
		files: nil,
	}, auditService)
	if err != nil {
		return nil, err
	}

	document, validation, policies, err := servedDocuments(openapi.Document, httpapi.ValidationDocument, cfg.Auth.Role() == config.RoleVerifier)
	if err != nil {
		return nil, err
	}

	router, err := httpapi.NewRouter(ctx, &httpapi.Options{
		Log:                 log,
		Translator:          translator,
		ServiceName:         cfg.Telemetry.ServiceName,
		ValidationDocument:  validation,
		Document:            document,
		DocsEnabled:         cfg.App.Docs(),
		CORSOrigins:         cfg.CORSOrigins(),
		AnyOrigin:           !cfg.App.IsProduction(),
		BodyLimit:           int64(cfg.HTTP.BodyLimit),
		TrustedProxies:      cfg.HTTP.TrustedProxies,
		Policies:            policies,
		Register:            v1.Register(server),
		Verifier:            keys.Verifier,
		Revocations:         revocations,
		Authorizer:          entity.RBAC{},
		Limiter:             limiter,
		RateLimits:          cfg.HTTP.RateLimitEnabled,
		Idempotency:         redis.NewIdempotency(infra.Redis),
		IdempotencyClaimTTL: max(cfg.App.ShutdownTimeout, httpserver.WriteTimeout) + _claimMargin,
		Transactor:          transactor{db: db},
		Auditor:             auditService,
		Readiness:           healthService,
		JWKS:                jwks,
		Metrics:             metrics,
		MetricsToken:        cfg.Metrics.Token.Value(),
		ReportError:         reportError(cfg),
		Now:                 nil,
	})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	return &Handler{Handler: router, health: healthService}, nil
}

// newHealth checks PostgreSQL and, when it is configured, Redis.
func newHealth(db *postgres.DB, infra *Infra) *health.Service {
	var redisPinger repo.Pinger
	if infra.Redis != nil {
		redisPinger = redis.NewPinger(infra.Redis)
	}

	return health.New(postgres.NewHealth(db), redisPinger)
}

// newServer builds the handlers of the documented operations and the JWKS document; a verifier
// serves no identity operation and publishes no JWKS.
func newServer(d *identityDeps, audits *audit.Service) (*v1.Server, []byte, error) {
	files, err := newFiles(d.cfg, d.db, d.log)
	if err != nil {
		return nil, nil, err
	}

	d.files = files

	deps := &v1.Deps{ //nolint:exhaustruct // the identity use cases are set below for the issuer
		Notes: note.New(postgres.NewNotes(d.db)),
		// modulegen:modules
		Files: files, Audits: audits, Log: d.log,
	}

	if d.cfg.Auth.Role() == config.RoleVerifier {
		return v1.New(deps), nil, nil
	}

	identity, err := newIdentity(d)
	if err != nil {
		return nil, nil, err
	}

	jwks, err := jwksDocument(d.keys)
	if err != nil {
		return nil, nil, err
	}

	deps.Auth, deps.Sessions, deps.Users = identity.Auth, identity.Sessions, identity.Users
	deps.SMS, deps.Accounts = identity.SMS, identity.Accounts

	return v1.New(deps), jwks, nil
}

// Serve runs the API server until ctx is canceled, then fails readiness and shuts down within
// SHUTDOWN_TIMEOUT.
func Serve(ctx context.Context, cfg *config.Config, log *slog.Logger, obs *Observability) error {
	infra, err := OpenInfra(ctx, cfg, cfg.Database.MaxConns)
	if err != nil {
		return err
	}
	defer infra.Close()

	metrics := obs.MetricsHandler()

	if metrics != nil && cfg.Metrics.Port != 0 {
		stop, err := serveMetrics(ctx, cfg, cfg.Metrics.Port, metrics, log)
		if err != nil {
			return err
		}
		defer stop()

		metrics = nil
	}

	handler, err := NewHandler(ctx, cfg, log, infra, metrics)
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
	handler.Drain()

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.App.ShutdownTimeout)
	defer cancel()

	return errors.Join(srv.Shutdown(shutdownCtx), <-serveErr)
}
