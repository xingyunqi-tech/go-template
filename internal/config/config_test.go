package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, config.EnvDevelopment, cfg.App.Env)
	assert.Equal(t, 3000, cfg.App.Port)
	assert.Equal(t, "zh", cfg.App.DefaultLocale)
	assert.Equal(t, 20*time.Second, cfg.App.ShutdownTimeout)
	assert.True(t, cfg.App.Docs())
	assert.Equal(t, "public", cfg.Database.Schema)
	assert.Equal(t, int32(20), cfg.Database.MaxConns)
	assert.Equal(t, int32(10), cfg.Database.WorkerMaxConns)
	assert.NotEmpty(t, cfg.Database.DSN())
	assert.False(t, cfg.Redis.Enabled())
	assert.Equal(t, config.ByteSize(1<<20), cfg.HTTP.BodyLimit)
	assert.True(t, cfg.HTTP.RateLimitEnabled)
	assert.Equal(t, 15*time.Minute, cfg.Auth.AccessTTL)
	assert.Equal(t, 720*time.Hour, cfg.Auth.RefreshTTL)
	assert.Equal(t, config.RoleEphemeralIssuer, cfg.Auth.Role())
	assert.Equal(t, "json", cfg.Log.Format)
	assert.Equal(t, "info", cfg.Log.Level)
	assert.True(t, cfg.Telemetry.Prometheus())
	assert.Equal(t, 9464, cfg.Metrics.WorkerPort)
}

func TestServiceNameIsRequired(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "OTEL_SERVICE_NAME")
}

func TestProductionRequirements(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("APP_ENV", "production")

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrInvalid)

	t.Setenv("RATE_LIMIT_ENABLED", "false")

	_, err = config.Load()
	require.ErrorIs(t, err, config.ErrInvalid)

	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "CORS_ORIGINS", "AUTH_SIGNING_KEY, AUTH_JWKS_URL", "METRICS_TOKEN, METRICS_PORT", "RATE_LIMIT_ENABLED", "FILE_URL_SECRET"} {
		assert.Contains(t, err.Error(), name)
	}

	t.Setenv("DATABASE_URL", "postgresql://u:p@db:5432/app")
	t.Setenv("REDIS_URL", "redis://redis:6379/0")
	t.Setenv("CORS_ORIGINS", "https://a.example.com, https://b.example.com")
	t.Setenv("AUTH_JWKS_URL", "https://auth.example.com/.well-known/jwks.json")
	t.Setenv("METRICS_PORT", "9100")
	t.Setenv("RATE_LIMIT_ENABLED", "true")
	t.Setenv("FILE_URL_SECRET", "test-url-secret-0123456789abcdef")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, config.RoleVerifier, cfg.Auth.Role())
	assert.Equal(t, []string{"https://a.example.com", "https://b.example.com"}, cfg.CORSOrigins())
	assert.False(t, cfg.App.Docs())
}

func TestProductionIssuerNeedsFrontendSMTPAndAliyunSMS(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_SIGNING_KEY", "pem")

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "APP_FRONTEND_URL")
	assert.Contains(t, err.Error(), "SMTP_URL")
	assert.Contains(t, err.Error(), "SMS_PROVIDER: must be aliyun")
}

func TestPartialCredentialsAreReported(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("WECHAT_MINIAPP_APP_ID", "wx")
	t.Setenv("WECHAT_APP_APP_SECRET", "s")
	t.Setenv("ALIPAY_APP_ID", "2021")
	t.Setenv("SMS_PROVIDER", "aliyun")

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrInvalid)

	for _, name := range []string{"WECHAT_MINIAPP_APP_ID", "WECHAT_APP_APP_ID", "ALIPAY_PUBLIC_KEY", "ALIYUN_SMS_SIGN_NAME"} {
		assert.Contains(t, err.Error(), name)
	}
}

func TestProvidersAreEnabledByTheirCredentials(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("WECHAT_MINIAPP_APP_ID", "wx")
	t.Setenv("WECHAT_MINIAPP_APP_SECRET", "s")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.Wechat.MiniApp())
	assert.False(t, cfg.Wechat.App())
	assert.False(t, cfg.Alipay.Enabled())
	assert.Equal(t, config.SMSLog, cfg.SMS.Provider)
	assert.Equal(t, 168*time.Hour, cfg.Account.DeletionGrace)
}

func TestSigningKeyAndJWKSURLAreExclusive(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("AUTH_SIGNING_KEY", "pem")
	t.Setenv("AUTH_JWKS_URL", "https://auth.example.com/.well-known/jwks.json")

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "only one of them")
}

func TestInvalidValuesAreReported(t *testing.T) {
	for name, value := range map[string]string{
		"DATABASE_SCHEMA":        "Bad-Schema",
		"BODY_LIMIT":             "1TB",
		"TRUSTED_PROXIES":        "nope",
		"LOG_FORMAT":             "xml",
		"OTEL_METRICS_EXPORTER":  "statsd",
		"METRICS_TOKEN":          "short",
		"AUTH_ACCESS_TTL":        "15",
		"APP_DEFAULT_LOCALE":     "fr",
		"SEED_ADMIN_PASSWORD":    "short",
		"SMS_PROVIDER":           "twilio",
		"ACCOUNT_DELETION_GRACE": "-1h",
		"STORAGE_DRIVER":         "ftp",
		"FILE_URL_SECRET":        "short",
		"AUDIT_RETENTION":        "0s",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("OTEL_SERVICE_NAME", "svc")
			t.Setenv(name, value)

			_, err := config.Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), name)
		})
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("BODY_LIMIT", "1TB")
	t.Setenv("LOG_FORMAT", "xml")
	t.Setenv("DATABASE_SCHEMA", "Bad-Schema")

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrInvalid)

	for _, name := range []string{"BODY_LIMIT", "LOG_FORMAT", "DATABASE_SCHEMA"} {
		assert.Contains(t, err.Error(), name)
	}
}

func TestFieldsSharingANameReportTheirOwnVariable(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("APP_PORT", "abc")

	_, err := config.Load()
	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(err.Error(), "APP_PORT"), err.Error())
	assert.NotContains(t, err.Error(), "METRICS_PORT")
}

func TestMissingRequiredVariableIsReportedOnce(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	os.Unsetenv("OTEL_SERVICE_NAME") //nolint:usetesting // t.Setenv restores it after the test

	_, err := config.Load()
	require.Error(t, err)
	assert.Equal(t, 1, strings.Count(err.Error(), "OTEL_SERVICE_NAME"), err.Error())
}

func TestByteSizeSuffixes(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]config.ByteSize{
		"512": 512, "64B": 64, "256KiB": 256 << 10, "1MiB": 1 << 20, "2 GiB": 2 << 30,
	} {
		var b config.ByteSize
		require.NoError(t, b.UnmarshalText([]byte(text)), text)
		assert.Equal(t, want, b, text)
	}

	for _, text := range []string{"", "0", "-1", "1MB", "x"} {
		var b config.ByteSize
		assert.Error(t, b.UnmarshalText([]byte(text)), text)
	}
}

func TestSecretIsRedacted(t *testing.T) {
	t.Parallel()

	s := config.Secret("hunter2")
	assert.Equal(t, "***", s.String())
	assert.Equal(t, "***", s.LogValue().String())
	assert.Equal(t, "hunter2", s.Value())
}

func TestS3NeedsABucketAndRegion(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "svc")
	t.Setenv("STORAGE_DRIVER", "s3")

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "S3_BUCKET, S3_REGION")

	t.Setenv("S3_BUCKET", "files")
	t.Setenv("S3_REGION", "us-east-1")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, time.Hour, cfg.Storage.URLTTL)
	assert.Equal(t, 4320*time.Hour, cfg.Audit.Retention)
}
