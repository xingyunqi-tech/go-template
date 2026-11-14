package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
)

func TestDatabaseTLSFollowsNodePostgres(t *testing.T) {
	t.Parallel()

	off := config.Database{Host: "db"}
	cfg, err := databaseTLS(&off)
	require.NoError(t, err)
	assert.Nil(t, cfg)

	var on config.Database

	on.Host = "db"
	require.NoError(t, on.SSLEnabled.UnmarshalText([]byte("true")))

	cfg, err = databaseTLS(&on)
	require.NoError(t, err)
	assert.Equal(t, "db", cfg.ServerName)
	assert.False(t, cfg.InsecureSkipVerify)

	require.NoError(t, on.RejectUnauthorized.UnmarshalText([]byte("false")))
	on.URL = "postgres://u:p@other:5432/x"

	cfg, err = databaseTLS(&on)
	require.NoError(t, err)
	assert.True(t, cfg.InsecureSkipVerify)
	assert.Equal(t, "other", cfg.ServerName)

	on.CA = "not pem"
	_, err = databaseTLS(&on)
	require.ErrorIs(t, err, errInvalidCA)

	on.CA, on.Cert = "", "bad"
	_, err = databaseTLS(&on)
	require.Error(t, err)
}

func TestRequiredColumnsComeFromTheNestjsSnapshot(t *testing.T) {
	t.Parallel()

	cols := requiredColumns()
	assert.Contains(t, cols, "user.email")
	assert.Contains(t, cols, "session.lastActiveAt")
	assert.Len(t, cols, 153)
}
