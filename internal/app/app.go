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
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/accountcleanup"
	"github.com/xingyunqi-tech/go-template/internal/usecase/address"
	"github.com/xingyunqi-tech/go-template/internal/usecase/alert"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appconfig"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appprofile"
	"github.com/xingyunqi-tech/go-template/internal/usecase/audit"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/internal/usecase/consent"
	"github.com/xingyunqi-tech/go-template/internal/usecase/favorite"
	"github.com/xingyunqi-tech/go-template/internal/usecase/feedback"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
	"github.com/xingyunqi-tech/go-template/internal/usecase/notification"
	"github.com/xingyunqi-tech/go-template/internal/usecase/permission"
	"github.com/xingyunqi-tech/go-template/internal/usecase/region"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/httpserver"
	"github.com/xingyunqi-tech/go-template/pkg/password"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
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

func newAccountCleanup(db *postgres.DB, jobs repo.Jobs, alerts *alert.Service, files *file.Service, log *slog.Logger) *accountcleanup.Service {
	cleanups := postgres.NewAccountCleanups(db)

	return accountcleanup.New(cleanups, jobs, alerts, log, accountcleanup.NewUsersCleaner(cleanups), file.NewCleaner(files),
		notification.NewCleaner(postgres.NewNotifications(db)),
		favorite.NewCleaner(postgres.NewFavorites(db)), feedback.NewCleaner(postgres.NewFeedbacks(db)),
		address.NewCleaner(postgres.NewAddresses(db)),
	)
}

func newHealth(cfg *config.Config, db *postgres.DB, infra *Infra) *health.Service {
	deps := health.Deps{
		Config: health.Config{
			WechatAppID: cfg.Wechat.AppID, WechatAppSecret: cfg.Wechat.AppSecret.Value(),
			AlipayAppID: cfg.Alipay.AppID,
		},
		Database: postgres.NewHealth(db), Redis: nil, HTTP: nil, DiskPath: "", Memory: nil, Disk: nil,
	}
	if infra.Redis != nil {
		deps.Redis = redis.NewPinger(infra.Redis)
	}

	return health.New(&deps)
}

func fileURLs(cfg *config.Config, files *file.Service) view.FileURLs {
	urls := view.FileURLs{BackendDomain: cfg.App.BackendDomain, APIPrefix: cfg.App.APIPrefix, Sign: nil}
	if cfg.File.Driver != config.FileDriverLocal {
		urls.Sign = files.URL
	}

	return urls
}

// rawRoutes are the file routes nestjs leaves out of the document; downloads exist only for the
// local driver.
func rawRoutes(server *v1.Server, driver string) []httpapi.RawRoute {
	routes := []httpapi.RawRoute{{
		ID: "readPrivateFile", Method: http.MethodGet, Pattern: "/api/v1/files/private/{id}",
		Params: []string{"id"}, Handler: server.ReadPrivateFile,
	}}

	if driver == config.FileDriverLocal {
		routes = append(routes, httpapi.RawRoute{
			ID: "downloadFile", Method: http.MethodGet, Pattern: "/api/v1/files/{path}",
			Params: []string{"path"}, Handler: server.DownloadFile,
		})
	}

	return routes
}

// Handler is the API handler.
type Handler struct {
	http.Handler

	health *health.Service
}

// Drain fails the readiness probe while the server shuts down.
func (h *Handler) Drain() {
	h.health.Drain()
}

// services are the usecases behind the HTTP API.
type services struct {
	db          *postgres.DB
	auth        *auth.Service
	users       *user.Service
	files       *file.Service
	health      *health.Service
	permissions *permission.Service
	limiter     *ratelimit.Limiter

	notifications *notification.Service
	cleanup       *accountcleanup.Service
	addresses     *address.Service
	favorites     *favorite.Service
	feedbacks     *feedback.Service
	config        *appconfig.Service
	regions       *region.Service
	appProfile    *appprofile.Service
	consents      *consent.Service
}

func newServices(cfg *config.Config, log *slog.Logger, infra *Infra) (*services, error) {
	hasher, err := password.New(cfg.Auth.PasswordHasher, cfg.Auth.HashConcurrency)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	inserter, err := newInserter(infra.Pool, log)
	if err != nil {
		return nil, err
	}

	db := postgres.NewDB(infra.Pool, acquireTimeout(cfg), log)
	alerts := alert.New(postgres.NewAlerts(db), infra.Metrics, log)
	cache := redis.NewCache(infra.Redis, log).WithCounter(infra.Metrics)
	jobs := postgres.NewJobs(inserter)
	files := newFiles(cfg, db, log)
	userRepo := postgres.NewUsers(db)
	sessionRepo := postgres.NewSessions(db)
	sessions := session.New(sessionRepo, cache, db, log)
	cleanup := newAccountCleanup(db, jobs, alerts, files, log)
	users := user.New(userRepo, postgres.NewFiles(db), sessions, hasher, cleanup, log)
	notifications := notification.New(postgres.NewNotifications(db), jobs, log)
	codes := newSMS(cfg, log, infra, cache)
	appConfig := appconfig.New(postgres.NewConfigs(db), cache, db, log)

	return &services{
		db: db,
		auth: auth.New(&auth.Deps{
			Config: authConfig(&cfg.Auth), Users: users, UserRepo: userRepo, Sessions: sessions, SessRepo: sessionRepo,
			Cache: cache, Tx: db, Hasher: hasher, Mailer: queueMailer{jobs: jobs}, SMS: codes, Log: log,
			Settings: notifications,
			Sender:   codes, Wechat: newWechat(cfg, log, infra, cache), Alipay: newAlipay(cfg, log, infra), Bindings: userRepo,
		}),
		users:       users,
		files:       files,
		health:      newHealth(cfg, db, infra),
		permissions: permission.New(postgres.NewPermissions(db), cache),
		limiter:     ratelimit.New(infra.Redis, throttleObserver{log: log, metrics: infra.Metrics, alerts: alerts}),

		notifications: notifications,
		cleanup:       cleanup,
		addresses:     address.New(postgres.NewAddresses(db), log),
		favorites:     favorite.New(postgres.NewFavorites(db)),
		feedbacks:     feedback.New(postgres.NewFeedbacks(db)),
		config:        appConfig,
		regions:       region.New(postgres.NewRegions(db), cache, log),
		appProfile:    appprofile.New(appConfig),
		consents:      consent.New(postgres.NewConsents(db), appConfig),
	}, nil
}

func routerOptions(cfg *config.Config, log *slog.Logger, infra *Infra, svc *services, server *v1.Server) *httpapi.Options {
	return &httpapi.Options{
		APIPrefix:      cfg.App.APIPrefix,
		SwaggerEnabled: cfg.App.Docs(),
		Docs:           openapi.Baseline,
		Validator:      nil,
		Translator:     nil,
		Log:            log,
		Production:     cfg.App.IsProduction(),
		FrontendDomain: cfg.App.FrontendDomain,
		Guards:         httpapi.AuthGuards(svc.auth),
		MonitorToken:   cfg.Metrics.Token.Value(),
		Permission:     httpapi.RequirePermissions(svc.permissions),
		Uploads: httpapi.UploadOptions{
			Files: svc.files, Permissions: svc.permissions, Presigned: cfg.File.Driver == config.FileDriverS3Presigned,
		},
		RawRoutes:   rawRoutes(server, cfg.File.Driver),
		Auditor:     audit.New(postgres.NewAudits(svc.db), log),
		Tracing:     cfg.Observability.OTelEnabled.Or(false),
		ReportError: reportError(cfg),
		Transactor:  transactor{db: svc.db},
		TrustProxy:  cfg.App.TrustProxy,
		Throttle: httpapi.ThrottleOptions{
			Enabled: cfg.Throttle.Disabled.On(), Limit: cfg.Throttle.Limit,
			TTL:          time.Duration(cfg.Throttle.TTL) * time.Second,
			AccessSecret: cfg.Auth.Secret.Value(), FileDriver: cfg.File.Driver, Limiter: svc.limiter,
		},
		NestHandlers: openapi.NestHandlers,
		Metrics:      infra.Metrics,
		MetricsPath:  cfg.Metrics.Path,
	}
}

// NewHandler builds the HTTP handler of the API on top of infra.
func NewHandler(cfg *config.Config, log *slog.Logger, infra *Infra) (*Handler, error) {
	translator, err := i18n.New(cfg.App.FallbackLanguage)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	validator, err := validatorx.New(openapi.Validation)
	if err != nil {
		return nil, fmt.Errorf("app: validation.json: %w", err)
	}

	svc, err := newServices(cfg, log, infra)
	if err != nil {
		return nil, err
	}

	server := v1.New(&v1.Deps{
		AppName: cfg.App.Name, Auth: svc.auth, Users: svc.users, URLs: fileURLs(cfg, svc.files),
		Health: svc.health, Log: log, Files: svc.files, PresignedUploads: cfg.File.Driver == config.FileDriverS3Presigned,
		Notifications: svc.notifications, Cleanup: svc.cleanup,
		Addresses: svc.addresses, Favorites: svc.favorites, Feedbacks: svc.feedbacks,
		Config: svc.config, Regions: svc.regions, AppProfile: svc.appProfile, Consents: svc.consents,
	})

	opts := routerOptions(cfg, log, infra, svc, server)
	opts.Validator = validator
	opts.Translator = translator

	handler, err := httpapi.NewRouter(server, opts)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	return &Handler{Handler: handler, health: svc.health}, nil
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
	handler.Drain()

	select {
	case <-time.After(cfg.App.DrainPeriod):
	case err := <-serveErr:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.App.ShutdownTimeout)
	defer cancel()

	return errors.Join(srv.Shutdown(shutdownCtx), <-serveErr)
}
