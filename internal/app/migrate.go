package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/xingyunqi-tech/go-template/db"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/migrations"
)

// ErrSchemaMismatch reports a database missing columns this service uses.
var ErrSchemaMismatch = errors.New("database schema is missing")

const _migrateConnections = 2

// Migrate applies the goose migrations, or with DB_SCHEMA_OWNER=external only checks that the
// shared database has every column of the nestjs schema.
func Migrate(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	infra, err := OpenInfra(ctx, cfg, _migrateConnections)
	if err != nil {
		return err
	}
	defer infra.Close()

	if cfg.Database.SchemaOwner == config.SchemaOwnerExternal {
		return CheckSchema(ctx, infra.Pool)
	}

	return MigrateUp(ctx, infra.Pool, log)
}

// MigrateUp applies all pending goose migrations.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("app: migrate: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("app: migrate: %w", err)
	}

	for _, r := range results {
		log.InfoContext(ctx, "migration applied", "version", r.Source.Version, "path", r.Source.Path, "duration_ms", r.Duration.Milliseconds())
	}

	return nil
}

// requiredColumns lists "table.column" from the nestjs schema snapshot.
func requiredColumns() []string {
	var out []string

	for line := range strings.SplitSeq(db.NestjsSchema, "\n") {
		rest, ok := strings.CutPrefix(line, "column ")
		if !ok {
			continue
		}

		name, _, _ := strings.Cut(rest, " ")
		out = append(out, name)
	}

	return out
}

// CheckSchema fails when a column of the nestjs schema is missing from the current schema.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `SELECT table_name || '.' || column_name FROM information_schema.columns WHERE table_schema = current_schema()`)
	if err != nil {
		return fmt.Errorf("app: check schema: %w", err)
	}
	defer rows.Close()

	existing := map[string]bool{}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return fmt.Errorf("app: check schema: %w", err)
		}

		existing[name] = true
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("app: check schema: %w", err)
	}

	var missing []string

	for _, c := range requiredColumns() {
		if !existing[c] {
			missing = append(missing, c)
		}
	}

	if len(missing) > 0 {
		slices.Sort(missing)

		return fmt.Errorf("%w: %s", ErrSchemaMismatch, strings.Join(missing, ", "))
	}

	return nil
}
