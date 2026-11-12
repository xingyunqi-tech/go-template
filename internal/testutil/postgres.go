//go:build integration

package testutil

import (
	"context"
	"fmt"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresImage is the server image used by integration tests.
const PostgresImage = "postgres:18.6-alpine"

const (
	_startupTimeout = 2 * time.Minute
	// The entrypoint restarts the server once after initdb.
	_readyLogOccurrences = 2
)

// Postgres is a running PostgreSQL container.
type Postgres struct {
	DSN       string
	container *tcpostgres.PostgresContainer
}

// StartPostgres starts a PostgreSQL container; failures are returned, never skipped.
func StartPostgres(ctx context.Context) (*Postgres, error) {
	UseDockerDesktopSocket()

	c, err := tcpostgres.Run(ctx, PostgresImage,
		tcpostgres.WithDatabase("app"),
		tcpostgres.WithUsername("app"),
		tcpostgres.WithPassword("app"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(_readyLogOccurrences).
				WithStartupTimeout(_startupTimeout),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("testutil: start postgres: %w", err)
	}

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(c)

		return nil, fmt.Errorf("testutil: postgres dsn: %w", err)
	}

	return &Postgres{DSN: dsn, container: c}, nil
}

// Terminate stops and removes the container.
func (p *Postgres) Terminate() error {
	if err := testcontainers.TerminateContainer(p.container); err != nil {
		return fmt.Errorf("testutil: terminate postgres: %w", err)
	}

	return nil
}
