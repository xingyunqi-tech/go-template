//go:build integration

package app_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

var (
	_db    *testutil.Database  //nolint:gochecknoglobals // one container per package
	_redis *testutil.RedisPool //nolint:gochecknoglobals // one container per package
)

func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"OTEL_SERVICE_NAME":    "app-test",
		"OTEL_TRACES_EXPORTER": "none",
		"OTEL_LOGS_EXPORTER":   "none",
		"AUTH_SIGNING_KEY":     testutil.SigningKeyPEM(),
		"AUTH_ISSUER":          testutil.Issuer,
		"AUTH_AUDIENCE":        testutil.Audience,
	} {
		_ = os.Setenv(k, v)
	}

	os.Exit(run(m))
}

func run(m *testing.M) int {
	db, err := testutil.StartDatabase(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		return 1
	}
	defer db.Close()

	redis, err := testutil.StartRedisPool(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		return 1
	}
	defer redis.Close()

	_db, _redis = db, redis

	return m.Run()
}
