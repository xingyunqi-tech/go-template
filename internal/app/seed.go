package app

import (
	"context"
	"log/slog"

	"github.com/xingyunqi-tech/go-template/internal/config"
)

// Seed inserts the demo or production accounts; the template has none yet.
func Seed(ctx context.Context, _ *config.Config, log *slog.Logger, _ *Observability) error {
	log.InfoContext(ctx, "seed completed", "rows", 0)

	return nil
}
