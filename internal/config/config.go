// Package config parses and validates the environment once at startup.
package config

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-playground/validator/v10"
)

// Environments accepted by APP_ENV.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

// ErrProduction reports settings that production refuses or that contradict each other.
var ErrProduction = errors.New("invalid settings")

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)

	return out
}

// Config is the whole application configuration.
type Config struct {
	App           App
	Log           Log
	Database      Database
	Redis         Redis
	Auth          Auth
	Seed          Seed
	Throttle      Throttle
	Metrics       Metrics
	File          File
	Mail          Mail
	Observability Observability
	SMS           SMS
	Wechat        Wechat
	Alipay        Alipay
}

// App holds settings shared with nestjs-template under the same variable names.
type App struct {
	Env              string        `env:"APP_ENV"               envDefault:"development" validate:"oneof=development test production"`
	Name             string        `env:"APP_NAME"              envDefault:"app"         validate:"required"`
	Port             int           `env:"APP_PORT"              envDefault:"3000"        validate:"min=1,max=65535"`
	APIPrefix        string        `env:"API_PREFIX"            envDefault:"api"         validate:"required,excludesall=/"`
	FallbackLanguage string        `env:"APP_FALLBACK_LANGUAGE" envDefault:"en"          validate:"oneof=en zh"`
	FrontendDomain   string        `env:"FRONTEND_DOMAIN"`
	BackendDomain    string        `env:"BACKEND_DOMAIN"        envDefault:"http://localhost"`
	TrustProxy       int           `env:"TRUST_PROXY"           envDefault:"0"           validate:"min=0"`
	SwaggerEnabled   OptionalBool  `env:"SWAGGER_ENABLED"`
	ShutdownTimeout  time.Duration `env:"SHUTDOWN_TIMEOUT"      envDefault:"30s"         validate:"gt=0"`
	DrainPeriod      time.Duration `env:"SHUTDOWN_DRAIN_PERIOD" envDefault:"0s"          validate:"gte=0"`
}

// Log configures the slog handler.
type Log struct {
	Level       string       `env:"LOG_LEVEL" validate:"omitempty,oneof=fatal error warn info debug trace silent"`
	PrettyPrint OptionalBool `env:"LOG_PRETTY_PRINT"`
}

// LevelOr returns LOG_LEVEL, or the default for the environment when unset: info in production,
// debug elsewhere.
func (l *Log) LevelOr(production bool) string {
	switch {
	case l.Level != "":
		return l.Level
	case production:
		return "info"
	default:
		return "debug"
	}
}

// IsProduction reports whether APP_ENV is production.
func (a *App) IsProduction() bool {
	return a.Env == EnvProduction
}

// Docs reports whether /docs and /docs-json are served; unset means every environment but production.
func (a *App) Docs() bool {
	return a.SwaggerEnabled.Or(!a.IsProduction())
}

// Load parses the environment and returns every problem at once.
func Load() (*Config, error) {
	var cfg Config

	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	validate := validator.New(validator.WithRequiredStructEnabled())
	validate.RegisterTagNameFunc(func(f reflect.StructField) string {
		if name := f.Tag.Get("env"); name != "" {
			return name
		}

		return f.Name
	})

	if err := validate.Struct(&cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	if problems := append(cfg.productionProblems(), cfg.infraProblems()...); len(problems) > 0 {
		return nil, fmt.Errorf("config: %w: %s", ErrProduction, strings.Join(problems, "; "))
	}

	return &cfg, nil
}
