//go:build integration

package testutil

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// RedisImage is the server image used by integration tests.
const RedisImage = "redis:8.10.3-alpine"

const (
	_redisDatabases    = 16
	_redisFlushTimeout = 10 * time.Second
)

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

// RedisPool is one Redis container whose logical databases 0–15 are lent to tests exclusively.
type RedisPool struct {
	redis *Redis
	dbs   chan int
}

// RedisDB is a borrowed logical database; URL gives it as REDIS_URL.
type RedisDB struct {
	Addr string
	DB   int
}

// StartRedisPool starts the package's Redis container.
func StartRedisPool(ctx context.Context) (*RedisPool, error) {
	ctx, cancel := context.WithTimeout(ctx, _setupTimeout)
	defer cancel()

	r, err := StartRedis(ctx)
	if err != nil {
		return nil, err
	}

	dbs := make(chan int, _redisDatabases)
	for i := range _redisDatabases {
		dbs <- i
	}

	return &RedisPool{redis: r, dbs: dbs}, nil
}

// Borrow blocks until a logical database is free, flushes it, and returns it flushed to the pool
// when the test ends.
func (p *RedisPool) Borrow(t *testing.T) RedisDB {
	t.Helper()

	var db int

	select {
	case db = <-p.dbs:
	case <-t.Context().Done():
		t.Fatalf("testutil: borrow redis db: %v", t.Context().Err())
	}

	d := RedisDB{Addr: p.redis.Addr, DB: db}

	if err := d.flush(t.Context()); err != nil {
		p.dbs <- db

		t.Fatalf("testutil: flush redis db %d: %v", db, err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), _redisFlushTimeout)
		defer cancel()

		if err := d.flush(ctx); err != nil {
			t.Errorf("testutil: flush redis db %d: %v", db, err)
		}

		p.dbs <- db
	})

	return d
}

// URL is the REDIS_URL of the database.
func (d RedisDB) URL() string {
	return "redis://" + d.Addr + "/" + strconv.Itoa(d.DB)
}

func (d RedisDB) flush(ctx context.Context) error {
	client := goredis.NewClient(&goredis.Options{Addr: d.Addr, DB: d.DB}) //nolint:exhaustruct // defaults
	defer func() { _ = client.Close() }()

	if err := client.FlushDB(ctx).Err(); err != nil {
		return fmt.Errorf("testutil: flushdb: %w", err)
	}

	return nil
}

// Close removes the container.
func (p *RedisPool) Close() {
	_ = p.redis.Terminate()
}
