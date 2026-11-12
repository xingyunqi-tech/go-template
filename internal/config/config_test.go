package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, config.EnvDevelopment, cfg.App.Env)
	assert.Equal(t, "app", cfg.App.Name)
	assert.Equal(t, 3000, cfg.App.Port)
	assert.Equal(t, "api", cfg.App.APIPrefix)
	assert.Equal(t, "en", cfg.App.FallbackLanguage)
	assert.Equal(t, 30*time.Second, cfg.App.ShutdownTimeout)
	assert.True(t, cfg.App.Docs())
}

func TestDocsDefaultsOffInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	cfg, err := config.Load()
	require.NoError(t, err)
	assert.False(t, cfg.App.Docs())

	t.Setenv("SWAGGER_ENABLED", "true")

	cfg, err = config.Load()
	require.NoError(t, err)
	assert.True(t, cfg.App.Docs())
}

func TestSwaggerEnabledOnlyTrueString(t *testing.T) {
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
	t.Setenv("APP_ENV", "staging")
	t.Setenv("APP_PORT", "70000")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APP_ENV")
	assert.Contains(t, err.Error(), "APP_PORT")
}

func TestLoadReportsEveryParseError(t *testing.T) {
	t.Setenv("APP_PORT", "abc")
	t.Setenv("SHUTDOWN_TIMEOUT", "soon")

	_, err := config.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"Port"`)
	assert.Contains(t, err.Error(), `"ShutdownTimeout"`)
	assert.NotContains(t, err.Error(), "'min' tag")
}
