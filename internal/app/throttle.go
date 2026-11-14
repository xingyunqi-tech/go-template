package app

import (
	"context"
	"log/slog"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/metrics"
)

const _throttlerFallbackAlertKey = "throttler-storage-fallback"

// alerter raises admin alerts.
type alerter interface {
	Raise(ctx context.Context, a entity.Alert)
}

// throttleObserver counts, logs and alerts on the throttler falling back to process memory.
type throttleObserver struct {
	log     *slog.Logger
	metrics *metrics.Metrics
	alerts  alerter
}

func (o throttleObserver) Fallback(ctx context.Context, err error, first bool) {
	o.metrics.ThrottlerFallbacks.Inc()

	if !first {
		return
	}

	reason := err.Error()

	o.log.ErrorContext(ctx, "redis throttler storage unavailable, falling back to in-memory storage", "error", reason)
	go o.alerts.Raise(context.WithoutCancel(ctx), entity.Alert{
		Type:     "throttler",
		Level:    entity.AlertCritical,
		Message:  "限流存储 Redis 不可用，已降级为单实例内存计数：" + reason,
		Payload:  map[string]any{"reason": reason},
		DedupKey: _throttlerFallbackAlertKey,
	})
}

func (o throttleObserver) Recovered(ctx context.Context) {
	o.log.InfoContext(ctx, "redis throttler storage recovered")
}
