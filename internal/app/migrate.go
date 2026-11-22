package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/migrations"
)

const _migrateConnections = 2

// migrationSets lists the goose directories of the authentication role, in apply order: a
// verifier has no identity tables.
func migrationSets(role config.Role) []migrations.Set {
	if role == config.RoleVerifier {
		return []migrations.Set{migrations.Core()}
	}

	return []migrations.Set{migrations.Core(), migrations.Identity()}
}

// Migrate creates DATABASE_SCHEMA and applies the goose and River migrations; down rolls them
// all back instead.
func Migrate(down bool) func(ctx context.Context, cfg *config.Config, log *slog.Logger, obs *Observability) error {
	return func(ctx context.Context, cfg *config.Config, log *slog.Logger, _ *Observability) error {
		infra, err := OpenInfra(ctx, cfg, _migrateConnections)
		if err != nil {
			return err
		}
		defer infra.Close()

		sets := migrationSets(cfg.Auth.Role())

		if down {
			return MigrateDown(ctx, infra.Pool, cfg.Database.Schema, sets, log)
		}

		return MigrateUp(ctx, infra.Pool, cfg.Database.Schema, sets, log)
	}
}

func provider(sqlDB *sql.DB, set migrations.Set) (*goose.Provider, error) {
	store, err := database.NewStore(database.DialectPostgres, set.Table)
	if err != nil {
		return nil, fmt.Errorf("app: migrate %s: %w", set.Name, err)
	}

	p, err := goose.NewProvider("", sqlDB, set.FS, goose.WithStore(store))
	if err != nil {
		return nil, fmt.Errorf("app: migrate %s: %w", set.Name, err)
	}

	return p, nil
}

// MigrateUp creates schema and applies the pending migrations of every set, then River's.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, schema string, sets []migrations.Set, log *slog.Logger) error {
	if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
		return fmt.Errorf("app: create schema: %w", err)
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	for _, set := range sets {
		p, err := provider(sqlDB, set)
		if err != nil {
			return err
		}

		results, err := p.Up(ctx)
		if err != nil {
			return fmt.Errorf("app: migrate %s: %w", set.Name, err)
		}

		for _, r := range results {
			log.InfoContext(ctx, "migration applied", "set", set.Name, "version", r.Source.Version, "duration_ms", r.Duration.Milliseconds())
		}
	}

	return postgres.MigrateRiver(ctx, pool, schema, log) //nolint:wrapcheck // wrapped by postgres
}

// MigrateDown rolls River back and then every set, last set first.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool, schema string, sets []migrations.Set, log *slog.Logger) error {
	if err := postgres.RollbackRiver(ctx, pool, schema, log); err != nil {
		return err //nolint:wrapcheck // wrapped by postgres
	}

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	for i := len(sets) - 1; i >= 0; i-- {
		p, err := provider(sqlDB, sets[i])
		if err != nil {
			return err
		}

		if _, err := p.DownTo(ctx, 0); err != nil {
			return fmt.Errorf("app: rollback %s: %w", sets[i].Name, err)
		}

		log.InfoContext(ctx, "migrations rolled back", "set", sets[i].Name)
	}

	return nil
}
