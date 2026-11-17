//go:build integration

package redis_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

var _redis *testutil.RedisPool //nolint:gochecknoglobals // one container per package

func TestMain(m *testing.M) {
	pool, err := testutil.StartRedisPool(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}

	_redis = pool
	code := m.Run()

	pool.Close()
	os.Exit(code)
}
