//go:build integration

package testutil

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// RedisImage is the server image used by integration tests.
const RedisImage = "redis:8.10.3-alpine"

// Redis is a running Redis container.
type Redis struct {
	Addr      string
	container *tcredis.RedisContainer
}

// StartRedis starts a Redis container; failures are returned, never skipped.
func StartRedis(ctx context.Context) (*Redis, error) {
	UseDockerDesktopSocket()

	c, err := tcredis.Run(ctx, RedisImage)
	if err != nil {
		return nil, fmt.Errorf("testutil: start redis: %w", err)
	}

	endpoint, err := c.Endpoint(ctx, "")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)

		return nil, fmt.Errorf("testutil: redis endpoint: %w", err)
	}

	return &Redis{Addr: endpoint, container: c}, nil
}

// Terminate stops and removes the container.
func (r *Redis) Terminate() error {
	if err := testcontainers.TerminateContainer(r.container); err != nil {
		return fmt.Errorf("testutil: terminate redis: %w", err)
	}

	return nil
}
