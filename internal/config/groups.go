package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strconv"

	"github.com/xingyunqi-tech/go-template/pkg/msduration"
)

const (
	_productionSecretMinLength = 32
	_redacted                  = "***"
)

// Password hashers accepted by PASSWORD_HASHER.
const (
	HasherArgon2id = "argon2id"
	HasherBcrypt   = "bcrypt"
)

// Schema owners accepted by DB_SCHEMA_OWNER.
const (
	SchemaOwnerGoose    = "goose"
	SchemaOwnerExternal = "external"
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

// Database configures the PostgreSQL connection with nestjs variable names.
type Database struct {
	URL                  string       `env:"DATABASE_URL"`
	Host                 string       `env:"DATABASE_HOST"                   envDefault:"localhost"`
	Port                 int          `env:"DATABASE_PORT"                   envDefault:"5432"  validate:"min=1,max=65535"`
	Username             string       `env:"DATABASE_USERNAME"               envDefault:"root"`
	Password             Secret       `env:"DATABASE_PASSWORD"               envDefault:"secret"`
	Name                 string       `env:"DATABASE_NAME"                   envDefault:"api"`
	MaxConnections       int32        `env:"DATABASE_MAX_CONNECTIONS"        envDefault:"80"    validate:"min=1"`
	WorkerMaxConnections int32        `env:"WORKER_DATABASE_MAX_CONNECTIONS" envDefault:"10"    validate:"min=1"`
	ConnectionTimeoutMS  int          `env:"DATABASE_CONNECTION_TIMEOUT_MS"  envDefault:"10000" validate:"min=1"`
	SSLEnabled           OptionalBool `env:"DATABASE_SSL_ENABLED"`
	RejectUnauthorized   OptionalBool `env:"DATABASE_REJECT_UNAUTHORIZED"`
	CA                   string       `env:"DATABASE_CA"`
	Key                  Secret       `env:"DATABASE_KEY"`
	Cert                 string       `env:"DATABASE_CERT"`
	SchemaOwner          string       `env:"DB_SCHEMA_OWNER"                 envDefault:"goose" validate:"oneof=goose external"`
}

// DSN returns DATABASE_URL, or a URL built from the separate variables.
func (d *Database) DSN() string {
	if d.URL != "" {
		return d.URL
	}

	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(d.Username, d.Password.Value()),
		Host:   d.Host + ":" + strconv.Itoa(d.Port),
		Path:   "/" + d.Name,
	}

	return u.String()
}

// Redis configures the Redis connection; only REDIS_ENABLED=true enables it.
type Redis struct {
	Enabled  OptionalBool `env:"REDIS_ENABLED"`
	Host     string       `env:"REDIS_HOST"     envDefault:"localhost"`
	Port     int          `env:"REDIS_PORT"     envDefault:"6379" validate:"min=1,max=65535"`
	Password Secret       `env:"REDIS_PASSWORD"`
	DB       int          `env:"REDIS_DB"       envDefault:"0"    validate:"min=0"`
}

// Addr returns host:port.
func (r *Redis) Addr() string {
	return r.Host + ":" + strconv.Itoa(r.Port)
}

// Auth holds the token secrets and lifetimes shared with nestjs.
type Auth struct {
	Secret              Secret           `env:"AUTH_JWT_SECRET,required"                     validate:"required"`
	Expires             msduration.Value `env:"AUTH_JWT_TOKEN_EXPIRES_IN"           envDefault:"15m"`
	RefreshSecret       Secret           `env:"AUTH_REFRESH_SECRET,required"                 validate:"required"`
	RefreshExpires      msduration.Value `env:"AUTH_REFRESH_TOKEN_EXPIRES_IN"       envDefault:"3650d"`
	ForgotSecret        Secret           `env:"AUTH_FORGOT_SECRET,required"                  validate:"required"`
	ForgotExpires       msduration.Value `env:"AUTH_FORGOT_TOKEN_EXPIRES_IN"        envDefault:"30m"`
	ConfirmEmailSecret  Secret           `env:"AUTH_CONFIRM_EMAIL_SECRET,required"           validate:"required"`
	ConfirmEmailExpires msduration.Value `env:"AUTH_CONFIRM_EMAIL_TOKEN_EXPIRES_IN" envDefault:"1d"`
	PasswordHasher      string           `env:"PASSWORD_HASHER"                     envDefault:"argon2id" validate:"oneof=argon2id bcrypt"`
	HashConcurrency     int              `env:"PASSWORD_HASH_CONCURRENCY"           envDefault:"2"        validate:"min=1"`
}

// Seed holds the production administrator created by the seed command.
type Seed struct {
	AdminEmail    string `env:"SEED_ADMIN_EMAIL"`
	AdminPassword Secret `env:"SEED_ADMIN_PASSWORD"`
}

// productionProblems lists settings production refuses.
func (c *Config) productionProblems() []string {
	if !c.App.IsProduction() {
		return nil
	}

	var problems []string

	secrets := []struct {
		name  string
		value string
	}{
		{"AUTH_JWT_SECRET", c.Auth.Secret.Value()},
		{"AUTH_REFRESH_SECRET", c.Auth.RefreshSecret.Value()},
		{"AUTH_FORGOT_SECRET", c.Auth.ForgotSecret.Value()},
		{"AUTH_CONFIRM_EMAIL_SECRET", c.Auth.ConfirmEmailSecret.Value()},
	}

	seen := map[string]bool{}
	duplicate := false

	for _, s := range secrets {
		if len(s.value) < _productionSecretMinLength {
			problems = append(problems, fmt.Sprintf("%s: must be at least %d characters in production", s.name, _productionSecretMinLength))
		}

		duplicate = duplicate || seen[s.value]
		seen[s.value] = true
	}

	if duplicate {
		problems = append(problems, "AUTH_JWT_SECRET, AUTH_REFRESH_SECRET, AUTH_FORGOT_SECRET, AUTH_CONFIRM_EMAIL_SECRET: must be different from each other in production")
	}

	if !c.Redis.Enabled.Or(false) {
		problems = append(problems, "REDIS_ENABLED: must be true in production")
	}

	return problems
}
