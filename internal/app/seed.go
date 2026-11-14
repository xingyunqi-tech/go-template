package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
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

	return nil
}
