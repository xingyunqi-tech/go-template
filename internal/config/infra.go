package config

import (
	"fmt"
	"strings"
)

// File drivers accepted by FILE_DRIVER.
const (
	FileDriverLocal       = "local"
	FileDriverS3          = "s3"
	FileDriverS3Presigned = "s3-presigned"
)

const _metricsTokenMinLength = 16

// Throttle is the default rate limit; only THROTTLE_ENABLED=false turns it off.
type Throttle struct {
	Disabled DisabledFlag `env:"THROTTLE_ENABLED"`
	TTL      int          `env:"THROTTLE_TTL"     envDefault:"60"  validate:"min=1"`
	Limit    int          `env:"THROTTLE_LIMIT"   envDefault:"100" validate:"min=1"`
}

// Metrics configures /metrics and the monitoring token; only METRICS_ENABLED=false disables
// request metrics.
type Metrics struct {
	Disabled DisabledFlag `env:"METRICS_ENABLED"`
	Path     string       `env:"METRICS_PATH"    envDefault:"metrics" validate:"required,excludesall=/"`
	Token    Secret       `env:"METRICS_TOKEN"`
	// WorkerPort is the worker process metrics listener; 0 disables it.
	WorkerPort int `env:"WORKER_METRICS_PORT" envDefault:"9464" validate:"min=0,max=65535"`
}

// File configures uploads; sizes are in bytes.
type File struct {
	Driver            string       `env:"FILE_DRIVER"                 envDefault:"local"    validate:"oneof=local s3 s3-presigned"`
	LocalRoot         string       `env:"FILE_LOCAL_ROOT"             envDefault:"files"    validate:"required"`
	AccessKeyID       string       `env:"ACCESS_KEY_ID"`
	SecretAccessKey   Secret       `env:"SECRET_ACCESS_KEY"`
	Bucket            string       `env:"AWS_DEFAULT_S3_BUCKET"`
	Region            string       `env:"AWS_S3_REGION"`
	Endpoint          string       `env:"AWS_S3_ENDPOINT"`
	PathStyleDisabled DisabledFlag `env:"AWS_S3_FORCE_PATH_STYLE"`
	ImageMaxSize      int64        `env:"FILE_IMAGE_MAX_SIZE"         envDefault:"5242880"  validate:"min=1"`
	PrivateImageMax   int64        `env:"FILE_PRIVATE_IMAGE_MAX_SIZE" envDefault:"10485760" validate:"min=1"`
	ContentImageMax   int64        `env:"FILE_CONTENT_IMAGE_MAX_SIZE" envDefault:"5242880"  validate:"min=1"`
	AudioMaxSize      int64        `env:"FILE_AUDIO_MAX_SIZE"         envDefault:"20971520" validate:"min=1"`
	ImportMaxSize     int64        `env:"FILE_IMPORT_MAX_SIZE"        envDefault:"52428800" validate:"min=1"`
	ExportMaxSize     int64        `env:"FILE_EXPORT_MAX_SIZE"        envDefault:"20971520" validate:"min=1"`
}

// Mail configures SMTP like nodemailer; without MAIL_HOST outside production mail is only logged.
type Mail struct {
	Host         string       `env:"MAIL_HOST"`
	Port         int          `env:"MAIL_PORT"          envDefault:"587" validate:"min=1,max=65535"`
	User         string       `env:"MAIL_USER"`
	Password     Secret       `env:"MAIL_PASSWORD"`
	DefaultEmail string       `env:"MAIL_DEFAULT_EMAIL"`
	DefaultName  string       `env:"MAIL_DEFAULT_NAME"`
	IgnoreTLS    OptionalBool `env:"MAIL_IGNORE_TLS"`
	Secure       OptionalBool `env:"MAIL_SECURE"`
	RequireTLS   OptionalBool `env:"MAIL_REQUIRE_TLS"`
}

// Observability configures tracing and Sentry.
type Observability struct {
	OTelEnabled  OptionalBool `env:"OTEL_ENABLED"`
	OTelEndpoint string       `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	SentryDSN    Secret       `env:"SENTRY_DSN"`
}

// DisabledFlag is a boolean that is on unless the variable is the string "false".
type DisabledFlag struct {
	off bool
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (f *DisabledFlag) UnmarshalText(text []byte) error {
	f.off = string(text) == "false"

	return nil
}

// On reports whether the flag is on.
func (f DisabledFlag) On() bool {
	return !f.off
}

func (c *Config) infraProblems() []string {
	var problems []string

	if token := c.Metrics.Token.Value(); token != "" && len(token) < _metricsTokenMinLength {
		problems = append(problems, fmt.Sprintf("METRICS_TOKEN: must be at least %d characters", _metricsTokenMinLength))
	}

	if c.File.Driver != FileDriverLocal {
		missing := unset(map[string]string{
			"ACCESS_KEY_ID": c.File.AccessKeyID, "SECRET_ACCESS_KEY": c.File.SecretAccessKey.Value(),
			"AWS_DEFAULT_S3_BUCKET": c.File.Bucket, "AWS_S3_REGION": c.File.Region,
		})
		if len(missing) > 0 {
			problems = append(problems, strings.Join(missing, ", ")+": required when FILE_DRIVER="+c.File.Driver)
		}
	}

	if c.App.IsProduction() {
		for _, name := range unset(map[string]string{
			"MAIL_HOST": c.Mail.Host, "MAIL_DEFAULT_EMAIL": c.Mail.DefaultEmail, "MAIL_DEFAULT_NAME": c.Mail.DefaultName,
		}) {
			problems = append(problems, name+": required in production")
		}
	}

	return sortedCopy(problems)
}

// unset returns the sorted names whose value is empty.
func unset(values map[string]string) []string {
	var names []string

	for name, v := range values {
		if v == "" {
			names = append(names, name)
		}
	}

	return sortedCopy(names)
}
