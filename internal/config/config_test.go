package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
)

func setSecrets(t *testing.T) {
	t.Helper()

	t.Setenv("AUTH_JWT_SECRET", "secret")
	t.Setenv("AUTH_REFRESH_SECRET", "secret_for_refresh")
	t.Setenv("AUTH_FORGOT_SECRET", "secret_for_forgot")
	t.Setenv("AUTH_CONFIRM_EMAIL_SECRET", "secret_for_confirm_email")
}

func TestLoadDefaults(t *testing.T) { //nolint:paralleltest // setSecrets uses t.Setenv
	setSecrets(t)

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, config.EnvDevelopment, cfg.App.Env)
	assert.Equal(t, "app", cfg.App.Name)
	assert.Equal(t, 3000, cfg.App.Port)
	assert.Equal(t, "api", cfg.App.APIPrefix)
	assert.Equal(t, "en", cfg.App.FallbackLanguage)
	assert.Equal(t, 30*time.Second, cfg.App.ShutdownTimeout)
	assert.True(t, cfg.App.Docs())
	assert.Equal(t, "postgres://root:secret@localhost:5432/api", cfg.Database.DSN())
	assert.Equal(t, int32(80), cfg.Database.MaxConnections)
	assert.Equal(t, int32(10), cfg.Database.WorkerMaxConnections)
	assert.Equal(t, "localhost:6379", cfg.Redis.Addr())
	assert.False(t, cfg.Redis.Enabled.Or(false))
	assert.InDelta(t, 900_000, cfg.Auth.Expires.Millis, 0)
	assert.Equal(t, "3650d", cfg.Auth.RefreshExpires.Text)
	assert.Equal(t, config.HasherArgon2id, cfg.Auth.PasswordHasher)
	assert.Equal(t, "***", cfg.Auth.Secret.String())
}

func TestDatabaseURLTakesPrecedence(t *testing.T) {
	setSecrets(t)
	t.Setenv("DATABASE_URL", "postgres://u:p@db:6543/x")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.Equal(t, "postgres://u:p@db:6543/x", cfg.Database.DSN())
}

func TestLoadRequiresAuthSecrets(t *testing.T) { //nolint:paralleltest // depends on the secrets being unset
	_, err := config.Load()
	require.Error(t, err)

	for _, name := range []string{"AUTH_JWT_SECRET", "AUTH_REFRESH_SECRET", "AUTH_FORGOT_SECRET", "AUTH_CONFIRM_EMAIL_SECRET"} {
		assert.Contains(t, err.Error(), name)
	}
}

func TestProductionRejectsWeakSettings(t *testing.T) {
	setSecrets(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("AUTH_REFRESH_SECRET", "secret")

	_, err := config.Load()
	require.ErrorIs(t, err, config.ErrProduction)
	assert.Contains(t, err.Error(), "AUTH_JWT_SECRET: must be at least 32 characters")
	assert.Contains(t, err.Error(), "must be different from each other")
	assert.Contains(t, err.Error(), "REDIS_ENABLED")

	for i, name := range []string{"AUTH_JWT_SECRET", "AUTH_REFRESH_SECRET", "AUTH_FORGOT_SECRET", "AUTH_CONFIRM_EMAIL_SECRET"} {
		t.Setenv(name, strings.Repeat(string(rune('a'+i)), 32))
	}

	t.Setenv("REDIS_ENABLED", "true")

	_, err = config.Load()
	require.ErrorIs(t, err, config.ErrProduction)
	assert.Contains(t, err.Error(), "MAIL_HOST: required in production")

	t.Setenv("MAIL_HOST", "smtp.example.com")
	t.Setenv("MAIL_DEFAULT_EMAIL", "noreply@example.com")
	t.Setenv("MAIL_DEFAULT_NAME", "App")

	_, err = config.Load()
	require.NoError(t, err)
}

func TestInfrastructureSettings(t *testing.T) {
	setSecrets(t)

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.Throttle.Disabled.On())
	assert.True(t, cfg.Metrics.Disabled.On())
	assert.Equal(t, "metrics", cfg.Metrics.Path)
	assert.Equal(t, config.FileDriverLocal, cfg.File.Driver)
	assert.True(t, cfg.File.PathStyleDisabled.On())
	assert.Equal(t, 587, cfg.Mail.Port)

	t.Setenv("THROTTLE_ENABLED", "0")
	t.Setenv("METRICS_ENABLED", "false")

	cfg, err = config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.Throttle.Disabled.On())
	assert.False(t, cfg.Metrics.Disabled.On())

	t.Setenv("METRICS_TOKEN", "short")
	t.Setenv("FILE_DRIVER", "s3")

	_, err = config.Load()
	require.ErrorIs(t, err, config.ErrProduction)
	assert.Contains(t, err.Error(), "METRICS_TOKEN: must be at least 16 characters")
	assert.Contains(t, err.Error(), "ACCESS_KEY_ID, AWS_DEFAULT_S3_BUCKET, AWS_S3_REGION, SECRET_ACCESS_KEY: required when FILE_DRIVER=s3")
}

func TestInvalidDurationIsRejected(t *testing.T) {
	setSecrets(t)
	t.Setenv("AUTH_JWT_TOKEN_EXPIRES_IN", "15 fortnights")

	_, err := config.Load()
	require.Error(t, err)
}

func TestDocsDefaultsOffInProduction(t *testing.T) {
	setSecrets(t)
	t.Setenv("APP_ENV", "development")

	cfg, err := config.Load()
	require.NoError(t, err)

	cfg.App.Env = config.EnvProduction
	assert.False(t, cfg.App.Docs())

	t.Setenv("SWAGGER_ENABLED", "true")

	cfg, err = config.Load()
	require.NoError(t, err)

	cfg.App.Env = config.EnvProduction
	assert.True(t, cfg.App.Docs())
}

func TestSwaggerEnabledOnlyTrueString(t *testing.T) {
	setSecrets(t)
	t.Setenv("SWAGGER_ENABLED", "1")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.App.Docs())
}

func TestLogLevelDefaultsByEnvironment(t *testing.T) {
	t.Parallel()

	var unset config.Log

	assert.Equal(t, "info", unset.LevelOr(true))
	assert.Equal(t, "debug", unset.LevelOr(false))

	set := config.Log{Level: "warn"}
	assert.Equal(t, "warn", set.LevelOr(true))
}

func TestLoadReportsEveryInvalidValue(t *testing.T) {
	setSecrets(t)
	t.Setenv("APP_ENV", "staging")
	t.Setenv("APP_PORT", "70000")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_ENV")
	assert.Contains(t, err.Error(), "APP_PORT")
}

func TestLoadReportsEveryParseError(t *testing.T) {
	setSecrets(t)
	t.Setenv("APP_PORT", "abc")
	t.Setenv("SHUTDOWN_TIMEOUT", "soon")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"Port"`)
	assert.Contains(t, err.Error(), `"ShutdownTimeout"`)
	assert.NotContains(t, err.Error(), "'min' tag")
}
