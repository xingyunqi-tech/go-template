// Package config parses and validates the environment once at startup.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"

	"github.com/xingyunqi-tech/go-template/pkg/clientip"
)

// Environments accepted by APP_ENV.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

// Metrics exporters accepted by OTEL_METRICS_EXPORTER.
const (
	MetricsPrometheus = "prometheus"
	MetricsOTLP       = "otlp"
	MetricsNone       = "none"
)

const (
	_redacted        = "***"
	_developmentDSN  = "postgresql://root:secret@localhost:5432/api?sslmode=disable" //nolint:gosec // local development database of make infra
	_minMetricsToken = 16
	_minPassword     = 8
	_maxPassword     = 128
)

var (
	// ErrInvalid reports settings that are malformed, contradict each other or are refused in
	// production.
	ErrInvalid = errors.New("invalid settings")

	_schemaName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
)

// Secret hides its value from logs and formatting.
type Secret string

// String implements fmt.Stringer.
func (s Secret) String() string {
	return _redacted
}

// LogValue implements slog.LogValuer.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(_redacted)
}

// Value returns the secret.
func (s Secret) Value() string {
	return string(s)
}

// Config is the whole application configuration.
type Config struct {
	App       App
	Log       Log
	Database  Database
	Redis     Redis
	Auth      Auth
	HTTP      HTTP
	Metrics   Metrics
	Telemetry Telemetry
	Mail      Mail
	SMS       SMS
	Wechat    Wechat
	Alipay    Alipay
	Account   Account
	Seed      Seed
}

// App holds the process settings.
type App struct {
	Env             string        `env:"APP_ENV"            envDefault:"development" validate:"oneof=development test production"`
	Port            int           `env:"APP_PORT"           envDefault:"3000"        validate:"min=1,max=65535"`
	DefaultLocale   string        `env:"APP_DEFAULT_LOCALE" envDefault:"zh"          validate:"oneof=zh en"`
	FrontendURL     string        `env:"APP_FRONTEND_URL"                            validate:"omitempty,url"`
	DocsEnabled     OptionalBool  `env:"DOCS_ENABLED"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT"   envDefault:"20s"         validate:"gt=0"`
}

// IsProduction reports whether APP_ENV is production.
func (a *App) IsProduction() bool {
	return a.Env == EnvProduction
}

// Docs reports whether /openapi.json and /docs are served; unset means every environment but
// production.
func (a *App) Docs() bool {
	return a.DocsEnabled.Or(!a.IsProduction())
}

// Log configures the slog handler.
type Log struct {
	Level  string `env:"LOG_LEVEL"  envDefault:"info" validate:"oneof=debug info warn error"`
	Format string `env:"LOG_FORMAT" envDefault:"json" validate:"oneof=json text"`
}

// Database configures the PostgreSQL connection.
type Database struct {
	URL            Secret `env:"DATABASE_URL"`
	Schema         string `env:"DATABASE_SCHEMA"           envDefault:"public"`
	MaxConns       int32  `env:"DATABASE_MAX_CONNS"        envDefault:"20" validate:"min=1"`
	WorkerMaxConns int32  `env:"WORKER_DATABASE_MAX_CONNS" envDefault:"10" validate:"min=1"`
}

// DSN returns DATABASE_URL, or the local development database when it is unset.
func (d *Database) DSN() string {
	if d.URL == "" {
		return _developmentDSN
	}

	return d.URL.Value()
}

// Redis configures the Redis connection; without REDIS_URL every Redis-backed feature runs in
// process.
type Redis struct {
	URL Secret `env:"REDIS_URL"`
}

// Enabled reports whether REDIS_URL is set.
func (r *Redis) Enabled() bool {
	return r.URL != ""
}

// Auth configures the access tokens; AUTH_SIGNING_KEY makes this service the issuer and
// AUTH_JWKS_URL a verifier.
type Auth struct {
	Issuer     string        `env:"AUTH_ISSUER"      envDefault:"go-template"`
	Audience   string        `env:"AUTH_AUDIENCE"    envDefault:"api"`
	SigningKey Secret        `env:"AUTH_SIGNING_KEY"`
	VerifyKeys string        `env:"AUTH_VERIFY_KEYS"`
	JWKSURL    string        `env:"AUTH_JWKS_URL"                         validate:"omitempty,url"`
	AccessTTL  time.Duration `env:"AUTH_ACCESS_TTL"  envDefault:"15m"     validate:"gt=0"`
	RefreshTTL time.Duration `env:"AUTH_REFRESH_TTL" envDefault:"720h"    validate:"gt=0"`
	// HashConcurrency bounds the argon2id computations running at once.
	HashConcurrency int `env:"PASSWORD_HASH_CONCURRENCY" envDefault:"2" validate:"min=1"`
}

// Mail configures the SMTP server of the issuing service; without SMTP_URL mails are only
// logged.
type Mail struct {
	SMTPURL Secret `env:"SMTP_URL"`
	From    string `env:"MAIL_FROM" envDefault:"no-reply@example.com" validate:"email"`
}

// SMS providers accepted by SMS_PROVIDER.
const (
	SMSLog    = "log"
	SMSAliyun = "aliyun"
)

// SMS selects how verification codes are sent: log only writes them to the log, aliyun sends
// them with Dysms.
type SMS struct {
	Provider        string `env:"SMS_PROVIDER"                 envDefault:"log"                           validate:"oneof=log aliyun"`
	AccessKeyID     string `env:"ALIYUN_SMS_ACCESS_KEY_ID"`
	AccessKeySecret Secret `env:"ALIYUN_SMS_ACCESS_KEY_SECRET"`
	SignName        string `env:"ALIYUN_SMS_SIGN_NAME"`
	TemplateCode    string `env:"ALIYUN_SMS_TEMPLATE_CODE"`
	Endpoint        string `env:"ALIYUN_SMS_ENDPOINT"          envDefault:"https://dysmsapi.aliyuncs.com" validate:"url"`
}

// Wechat holds the mini program and open platform app credentials; a provider is enabled when
// both of its variables are set.
type Wechat struct {
	MiniAppID     string `env:"WECHAT_MINIAPP_APP_ID"`
	MiniAppSecret Secret `env:"WECHAT_MINIAPP_APP_SECRET"`
	AppID         string `env:"WECHAT_APP_APP_ID"`
	AppSecret     Secret `env:"WECHAT_APP_APP_SECRET"`
	APIBase       string `env:"WECHAT_API_BASE"            envDefault:"https://api.weixin.qq.com" validate:"url"`
}

// MiniApp reports whether wechat_miniapp is enabled.
func (w *Wechat) MiniApp() bool {
	return w.MiniAppID != "" && w.MiniAppSecret != ""
}

// App reports whether wechat_app is enabled.
func (w *Wechat) App() bool {
	return w.AppID != "" && w.AppSecret != ""
}

// Alipay holds what alipay.system.oauth.token needs; the provider is enabled when the app id
// and both keys are set.
type Alipay struct {
	AppID      string `env:"ALIPAY_APP_ID"`
	PrivateKey Secret `env:"ALIPAY_PRIVATE_KEY"`
	PublicKey  string `env:"ALIPAY_PUBLIC_KEY"`
	Gateway    string `env:"ALIPAY_GATEWAY"     envDefault:"https://openapi.alipay.com/gateway.do" validate:"url"`
}

// Enabled reports whether alipay is enabled.
func (a *Alipay) Enabled() bool {
	return a.AppID != "" && a.PrivateKey != "" && a.PublicKey != ""
}

// Account configures account deletion.
type Account struct {
	DeletionGrace time.Duration `env:"ACCOUNT_DELETION_GRACE" envDefault:"168h" validate:"gte=0"`
}

// Seed holds the administrator the production seed creates when no active admin exists.
type Seed struct {
	AdminEmail    string `env:"SEED_ADMIN_EMAIL"    validate:"omitempty,email"`
	AdminPassword Secret `env:"SEED_ADMIN_PASSWORD"`
}

// Role is how this service takes part in authentication.
type Role int

// Authentication roles.
const (
	// RoleIssuer signs tokens with AUTH_SIGNING_KEY.
	RoleIssuer Role = iota
	// RoleVerifier only verifies tokens with the keys at AUTH_JWKS_URL.
	RoleVerifier
	// RoleEphemeralIssuer signs tokens with a key generated at startup, outside production.
	RoleEphemeralIssuer
)

// Role returns the authentication role selected by the auth variables.
func (a *Auth) Role() Role {
	switch {
	case a.SigningKey != "":
		return RoleIssuer
	case a.JWKSURL != "":
		return RoleVerifier
	default:
		return RoleEphemeralIssuer
	}
}

// HTTP configures request handling.
type HTTP struct {
	BodyLimit        ByteSize         `env:"BODY_LIMIT"         envDefault:"1MiB"`
	CORSOrigins      []string         `env:"CORS_ORIGINS"       envSeparator:","`
	TrustedProxies   clientip.Proxies `env:"TRUSTED_PROXIES"    envDefault:"0"`
	RateLimitEnabled bool             `env:"RATE_LIMIT_ENABLED" envDefault:"true"`
}

// Metrics configures the Prometheus endpoints.
type Metrics struct {
	Token      Secret `env:"METRICS_TOKEN"`
	Port       int    `env:"METRICS_PORT"                         validate:"min=0,max=65535"`
	WorkerPort int    `env:"WORKER_METRICS_PORT" envDefault:"9464" validate:"min=0,max=65535"`
}

// Telemetry holds the OpenTelemetry and Sentry settings this service reads itself; the other
// OTEL_* variables go straight to the SDK.
type Telemetry struct {
	ServiceName     string `env:"OTEL_SERVICE_NAME,required"                  validate:"required"`
	MetricsExporter string `env:"OTEL_METRICS_EXPORTER"   envDefault:"prometheus" validate:"oneof=prometheus otlp none"`
	SDKDisabled     bool   `env:"OTEL_SDK_DISABLED"`
	SentryDSN       Secret `env:"SENTRY_DSN"`
}

// Prometheus reports whether metrics are pulled from /metrics.
func (t *Telemetry) Prometheus() bool {
	return t.MetricsExporter == MetricsPrometheus && !t.SDKDisabled
}

// Load parses the environment and returns every problem at once.
func Load() (*Config, error) {
	var (
		cfg  Config
		errs []error
	)

	failed, err := parseEnv(&cfg)
	if err != nil {
		errs = append(errs, err)
	}

	validate := validator.New(validator.WithRequiredStructEnabled())
	validate.RegisterTagNameFunc(func(f reflect.StructField) string {
		if name := f.Tag.Get("env"); name != "" {
			name, _, _ = strings.Cut(name, ",")

			return name
		}

		return f.Name
	})

	if err := validate.Struct(&cfg); err != nil {
		errs = append(errs, validationErrors(err, failed))
	}

	if problems := cfg.problems(); len(problems) > 0 {
		errs = append(errs, fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; ")))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) problems() []string {
	var problems []string

	if !_schemaName.MatchString(c.Database.Schema) {
		problems = append(problems, "DATABASE_SCHEMA: must match "+_schemaName.String())
	}

	if c.Auth.SigningKey != "" && c.Auth.JWKSURL != "" {
		problems = append(problems, "AUTH_SIGNING_KEY, AUTH_JWKS_URL: only one of them may be set")
	}

	if pw := c.Seed.AdminPassword.Value(); pw != "" && (utf8.RuneCountInString(pw) < _minPassword || utf8.RuneCountInString(pw) > _maxPassword) {
		problems = append(problems, fmt.Sprintf("SEED_ADMIN_PASSWORD: must have %d to %d characters", _minPassword, _maxPassword))
	}

	problems = append(problems, c.integrationProblems()...)

	if token := c.Metrics.Token.Value(); token != "" && len(token) < _minMetricsToken {
		problems = append(problems, fmt.Sprintf("METRICS_TOKEN: must be at least %d characters", _minMetricsToken))
	}

	if c.App.IsProduction() {
		problems = append(problems, c.productionProblems()...)
	}

	slices.Sort(problems)

	return problems
}

func (c *Config) productionProblems() []string {
	var problems []string

	required := map[string]bool{
		"DATABASE_URL": c.Database.URL != "",
		"REDIS_URL":    c.Redis.Enabled(),
		"CORS_ORIGINS": len(c.CORSOrigins()) > 0,
	}

	for name, ok := range required {
		if !ok {
			problems = append(problems, name+": required in production")
		}
	}

	if !c.HTTP.RateLimitEnabled {
		problems = append(problems, "RATE_LIMIT_ENABLED: must be true in production")
	}

	problems = append(problems, c.authProblems()...)

	if c.Telemetry.Prometheus() && c.Metrics.Token == "" && c.Metrics.Port == 0 {
		problems = append(problems, "METRICS_TOKEN, METRICS_PORT: one of them is required in production when OTEL_METRICS_EXPORTER=prometheus")
	}

	return problems
}

// authProblems are the production requirements of the authentication role.
func (c *Config) authProblems() []string {
	switch c.Auth.Role() {
	case RoleEphemeralIssuer:
		return []string{"AUTH_SIGNING_KEY, AUTH_JWKS_URL: one of them is required in production"}
	case RoleIssuer:
		var problems []string

		if c.App.FrontendURL == "" {
			problems = append(problems, "APP_FRONTEND_URL: required in production for the issuing service")
		}

		if c.Mail.SMTPURL == "" {
			problems = append(problems, "SMTP_URL: required in production for the issuing service")
		}

		if c.SMS.Provider != SMSAliyun {
			problems = append(problems, "SMS_PROVIDER: must be aliyun in production for the issuing service")
		}

		return problems
	case RoleVerifier:
	}

	return nil
}

// integrationProblems reports credentials that are set only in part.
func (c *Config) integrationProblems() []string {
	var problems []string

	partial := func(names string, set ...bool) {
		n := 0

		for _, ok := range set {
			if ok {
				n++
			}
		}

		if n > 0 && n < len(set) {
			problems = append(problems, names+": set all of them or none")
		}
	}

	partial("WECHAT_MINIAPP_APP_ID, WECHAT_MINIAPP_APP_SECRET", c.Wechat.MiniAppID != "", c.Wechat.MiniAppSecret != "")
	partial("WECHAT_APP_APP_ID, WECHAT_APP_APP_SECRET", c.Wechat.AppID != "", c.Wechat.AppSecret != "")
	partial("ALIPAY_APP_ID, ALIPAY_PRIVATE_KEY, ALIPAY_PUBLIC_KEY", c.Alipay.AppID != "", c.Alipay.PrivateKey != "", c.Alipay.PublicKey != "")

	if c.SMS.Provider == SMSAliyun && (c.SMS.AccessKeyID == "" || c.SMS.AccessKeySecret == "" || c.SMS.SignName == "" || c.SMS.TemplateCode == "") {
		problems = append(problems, "ALIYUN_SMS_ACCESS_KEY_ID, ALIYUN_SMS_ACCESS_KEY_SECRET, ALIYUN_SMS_SIGN_NAME, ALIYUN_SMS_TEMPLATE_CODE: required when SMS_PROVIDER=aliyun")
	}

	return problems
}

// parseEnv parses every settings group of cfg, names the variable of every field that failed and
// returns those variables.
func parseEnv(cfg *Config) (map[string]bool, error) {
	failed := map[string]bool{}

	var errs []error

	groups := reflect.ValueOf(cfg).Elem()

	for i := range groups.NumField() {
		group := groups.Field(i)

		if err := env.Parse(group.Addr().Interface()); err != nil {
			errs = append(errs, groupErrors(err, envNames(group.Type()), failed))
		}
	}

	return failed, errors.Join(errs...)
}

// groupErrors names the variable of every field of one group in err and records it in failed.
func groupErrors(err error, names map[string]string, failed map[string]bool) error {
	var agg env.AggregateError
	if !errors.As(err, &agg) {
		return err
	}

	errs := make([]error, 0, len(agg.Errors))

	for _, e := range agg.Errors {
		var (
			parse  env.ParseError
			notSet env.VarIsNotSetError
		)

		switch {
		case errors.As(e, &parse) && names[parse.Name] != "":
			failed[names[parse.Name]] = true
			e = fmt.Errorf("%s: %w", names[parse.Name], parse.Err)
		case errors.As(e, &notSet):
			failed[notSet.Key] = true
		}

		errs = append(errs, e)
	}

	return errors.Join(errs...)
}

// validationErrors drops the validator errors of the variables that failed to parse.
func validationErrors(err error, failed map[string]bool) error {
	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return err
	}

	errs := make([]error, 0, len(verrs))

	for _, e := range verrs {
		if !failed[e.Field()] {
			errs = append(errs, e)
		}
	}

	return errors.Join(errs...)
}

// envNames maps the field names of one settings group to their variables.
func envNames(t reflect.Type) map[string]string {
	names := map[string]string{}

	for f := range t.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("env"), ","); name != "" {
			names[f.Name] = name
		} else if f.Type.Kind() == reflect.Struct {
			maps.Copy(names, envNames(f.Type))
		}
	}

	return names
}

// CORSOrigins returns CORS_ORIGINS without blank entries.
func (c *Config) CORSOrigins() []string {
	var origins []string

	for _, o := range c.HTTP.CORSOrigins {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}

	return origins
}
