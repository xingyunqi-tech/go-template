//go:build integration

package testutil

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/migrations"
)

const (
	_templateDB   = "app_template"
	_setupTimeout = 2 * time.Minute
)

// Database is a PostgreSQL container with a migrated template database that tests
// clone into private databases.
type Database struct {
	pg    *Postgres
	admin *pgxpool.Pool
	mu    sync.Mutex
	n     atomic.Int64
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("testutil: parse dsn: %w", err)
	}

	u.Path = "/" + name

	return u.String(), nil
}

// StartDatabase starts PostgreSQL and prepares the template database.
func StartDatabase(ctx context.Context) (*Database, error) {
	ctx, cancel := context.WithTimeout(ctx, _setupTimeout)
	defer cancel()

	pg, err := StartPostgres(ctx)
	if err != nil {
		return nil, err
	}

	admin, err := pgxpool.New(ctx, pg.DSN)
	if err != nil {
		_ = pg.Terminate()

		return nil, fmt.Errorf("testutil: connect: %w", err)
	}

	d := &Database{pg: pg, admin: admin, mu: sync.Mutex{}, n: atomic.Int64{}}

	if err := d.prepareTemplate(ctx); err != nil {
		d.Close()

		return nil, err
	}

	return d, nil
}

func (d *Database) prepareTemplate(ctx context.Context) error {
	if _, err := d.admin.Exec(ctx, "CREATE DATABASE "+_templateDB); err != nil {
		return fmt.Errorf("testutil: create template: %w", err)
	}

	dsn, err := withDatabase(d.pg.DSN, _templateDB)
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("testutil: connect template: %w", err)
	}
	defer pool.Close()

	log := slog.New(slog.DiscardHandler)

	return app.MigrateUp(ctx, pool, "public", []migrations.Set{migrations.Core()}, log) //nolint:wrapcheck // migration errors are wrapped
}

// Clone creates a private copy of the template database and returns its DSN; it is dropped
// when the test ends.
func (d *Database) Clone(t *testing.T) string {
	t.Helper()

	name := "t_" + strconv.FormatInt(d.n.Add(1), 10)

	d.mu.Lock()
	_, err := d.admin.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE "+_templateDB)
	d.mu.Unlock()

	if err != nil {
		t.Fatalf("testutil: clone database: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), time.Minute)
		defer cancel()

		if _, err := d.admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Logf("testutil: drop database %s: %v", name, err)
		}
	})

	dsn, err := withDatabase(d.pg.DSN, name)
	if err != nil {
		t.Fatal(err)
	}

	return dsn
}

// Close removes the container.
func (d *Database) Close() {
	d.admin.Close()
	_ = d.pg.Terminate()
}
