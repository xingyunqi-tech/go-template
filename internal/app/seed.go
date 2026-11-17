package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/usecase/permission"
	"github.com/xingyunqi-tech/go-template/pkg/password"
	"github.com/xingyunqi-tech/go-template/seeds"
)

// Seed inserts the reference data and the demo or production administrator accounts.
func Seed(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	infra, err := OpenInfra(ctx, cfg, _migrateConnections)
	if err != nil {
		return err
	}
	defer infra.Close()

	hasher, err := password.New(cfg.Auth.PasswordHasher, cfg.Auth.HashConcurrency)
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	db := postgres.NewDB(infra.Pool, acquireTimeout(cfg), log)

	err = db.Seed(ctx, seeds.Data, seeds.Regions, postgres.SeedOptions{
		Production:    cfg.App.IsProduction(),
		AdminEmail:    cfg.Seed.AdminEmail,
		AdminPassword: cfg.Seed.AdminPassword.Value(),
		Hash:          hasher.Hash,
	})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}

	log.InfoContext(ctx, "seed completed")

	if err := clearPermissionCache(ctx, infra, log); err != nil {
		log.WarnContext(ctx, "permission cache not cleared; cached role permissions expire within 5 minutes", "error", err.Error())
	}

	return nil
}

// ClearPermissionCache deletes the cached role permission map.
func ClearPermissionCache(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	infra, err := OpenInfra(ctx, cfg, _migrateConnections)
	if err != nil {
		return err
	}
	defer infra.Close()

	return clearPermissionCache(ctx, infra, log)
}

func clearPermissionCache(ctx context.Context, infra *Infra, log *slog.Logger) error {
	if infra.Redis == nil {
		log.WarnContext(ctx, "REDIS_ENABLED=false: each process caches role permissions in memory for up to 5 minutes")

		return nil
	}

	if err := infra.Redis.Del(ctx, permission.CacheKey).Err(); err != nil {
		return fmt.Errorf("app: clear permission cache: %w", err)
	}

	log.InfoContext(ctx, "permission cache cleared")

	return nil
}

// CreateSuperuser returns the command creating an active administrator with email and password.
func CreateSuperuser(email, pass string) func(context.Context, *config.Config, *slog.Logger) error {
	return func(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
		infra, err := OpenInfra(ctx, cfg, _migrateConnections)
		if err != nil {
			return err
		}
		defer infra.Close()

		hasher, err := password.New(cfg.Auth.PasswordHasher, cfg.Auth.HashConcurrency)
		if err != nil {
			return fmt.Errorf("app: %w", err)
		}

		if err := postgres.NewDB(infra.Pool, acquireTimeout(cfg), log).CreateSuperuser(ctx, email, pass, hasher.Hash); err != nil {
			return fmt.Errorf("app: %w", err)
		}

		log.InfoContext(ctx, "superuser created", "email", email)

		return nil
	}
}
