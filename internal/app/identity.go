package app

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/events"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/alipay"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
	smsapi "github.com/xingyunqi-tech/go-template/internal/repo/webapi/sms"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/wechat"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	accountuc "github.com/xingyunqi-tech/go-template/internal/usecase/account"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
	"github.com/xingyunqi-tech/go-template/pkg/password"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
	"github.com/xingyunqi-tech/go-template/templates"
)

// Identity holds the use cases of the issuing service.
type Identity struct {
	Auth     *auth.Service
	Sessions *session.Service
	Users    *user.Service
	SMS      *sms.Service
	Accounts *accountuc.Service
}

// identityDeps are the infrastructure the identity use cases share with the router.
type identityDeps struct {
	cfg        *config.Config
	log        *slog.Logger
	db         *postgres.DB
	infra      *Infra
	keys       *Keys
	translator *i18n.Translator
	limiter    *ratelimit.Limiter
	revoker    session.Revoker
	files      *file.Service
}

func newMailer(cfg *config.Config, log *slog.Logger) (repo.Mailer, error) {
	if cfg.Mail.SMTPURL == "" {
		return mail.NewLog(log), nil
	}

	m, err := mail.NewSMTP(cfg.Mail.SMTPURL.Value(), cfg.Mail.From)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	return m, nil
}

// newInsertClient returns the River client the API inserts jobs with.
func newInsertClient(cfg *config.Config, log *slog.Logger, infra *Infra) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(infra.Pool), postgres.RiverConfig(cfg.Database.Schema, log))
	if err != nil {
		return nil, fmt.Errorf("app: river: %w", err)
	}

	return client, nil
}

func newSMSSender(cfg *config.Config, log *slog.Logger) repo.SMSSender {
	if cfg.SMS.Provider != config.SMSAliyun {
		return smsapi.NewLog(log)
	}

	return smsapi.New(&smsapi.Config{
		AccessKeyID: cfg.SMS.AccessKeyID, AccessKeySecret: cfg.SMS.AccessKeySecret.Value(), SignName: cfg.SMS.SignName,
		TemplateCode: cfg.SMS.TemplateCode, Endpoint: cfg.SMS.Endpoint,
	}, nil)
}

// newProviders returns the providers whose credentials are set and the mini program phone
// resolver, nil without wechat_miniapp.
func newProviders(cfg *config.Config, cache repo.Cache) ([]auth.Provider, user.WechatPhone, error) {
	var (
		providers []auth.Provider
		phone     user.WechatPhone
	)

	client := wechat.New(cfg.Wechat.APIBase, cache, nil)

	if cfg.Wechat.MiniApp() {
		mini := wechat.NewMiniApp(client, wechat.Credentials{AppID: cfg.Wechat.MiniAppID, AppSecret: cfg.Wechat.MiniAppSecret.Value()})
		providers, phone = append(providers, mini), mini
	}

	if cfg.Wechat.App() {
		providers = append(providers, wechat.NewApp(client, wechat.Credentials{AppID: cfg.Wechat.AppID, AppSecret: cfg.Wechat.AppSecret.Value()}))
	}

	if cfg.Alipay.Enabled() {
		ali, err := alipay.New(&alipay.Config{
			AppID: cfg.Alipay.AppID, PrivateKey: cfg.Alipay.PrivateKey.Value(), PublicKey: cfg.Alipay.PublicKey, Gateway: cfg.Alipay.Gateway,
		}, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("app: %w", err)
		}

		providers = append(providers, ali)
	}

	return providers, phone, nil
}

func newIdentity(d *identityDeps) (*Identity, error) {
	hasher, err := password.New(password.Default(), d.cfg.Auth.HashConcurrency)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	renderer, err := templates.NewMail(d.translator)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	mailer, err := newMailer(d.cfg, d.log)
	if err != nil {
		return nil, err
	}

	inserter, err := newInsertClient(d.cfg, d.log, d.infra)
	if err != nil {
		return nil, err
	}

	providers, wechatPhone, err := newProviders(d.cfg, redis.NewCache(d.infra.Redis, d.cfg.Telemetry.ServiceName, d.log))
	if err != nil {
		return nil, err
	}

	users, sessions, identities := postgres.NewUsers(d.db), postgres.NewSessions(d.db), postgres.NewIdentities(d.db)
	revocation := session.NewRevocation(sessions, d.revoker, d.cfg.Auth.AccessTTL)
	smsService := sms.New(redis.NewSMSCodes(d.infra.Redis, d.cfg.Telemetry.ServiceName, sms.Limits()), newSMSSender(d.cfg, d.log), nil)
	limits := usecase.Limits{Limiter: nil, Enabled: d.cfg.HTTP.RateLimitEnabled}

	if d.limiter != nil {
		limits.Limiter = d.limiter
	}

	var signer auth.Signer
	if d.keys != nil && d.keys.Signer != nil {
		signer = d.keys.Signer
	}

	authService := auth.New(auth.Config{
		AccessTTL: d.cfg.Auth.AccessTTL, RefreshTTL: d.cfg.Auth.RefreshTTL, FrontendURL: d.cfg.App.FrontendURL,
	}, &auth.Deps{
		Users: users, Identities: identities, Sessions: sessions, Tokens: postgres.NewTokens(d.db), MailQueue: postgres.NewMailQueue(inserter),
		Mailer: mailer, Renderer: renderer, Grace: redis.NewRefreshGrace(d.infra.Redis, d.cfg.Telemetry.ServiceName),
		Revocation: revocation, Signer: signer, Codes: smsService, Providers: providers, Hasher: hasher, Limits: limits,
		Tx: d.db, Log: d.log, Now: nil,
	})

	return &Identity{
		Auth:     authService,
		Sessions: session.New(sessions, revocation),
		Users: user.New(&user.Deps{
			Avatars: d.files, Users: users, Codes: smsService, WechatPhone: wechatPhone, Revocation: revocation, Hasher: hasher, Limits: limits,
			Now: nil, Outside: postgres.WithoutTx,
		}),
		SMS: smsService,
		Accounts: accountuc.New(&accountuc.Deps{
			Users: users, Identities: identities, Revocation: revocation, Queue: postgres.NewAccountQueue(inserter),
			Unpurged:  postgres.NewMaintenance(d.db),
			Purgers:   corePurgers(d.db, d.files),
			Publisher: events.NewLog(d.log), Tx: d.db, Log: d.log, Grace: d.cfg.Account.DeletionGrace,
			Source: d.cfg.Telemetry.ServiceName, Now: nil,
		}),
	}, nil
}

// jwksDocument is the JWKS the issuing service publishes.
func jwksDocument(keys *Keys) ([]byte, error) {
	if keys.Static == nil {
		return nil, nil
	}

	doc, err := json.Marshal(keys.Static.Set())
	if err != nil {
		return nil, fmt.Errorf("app: jwks: %w", err)
	}

	return doc, nil
}
