// Package alert is nestjs AlertService: deduplicated admin alerts with in-app notifications.
package alert

import (
	"context"
	"log/slog"
	"unicode/utf16"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_recipientLimit = 50
	_titleMaxLength = 200
	_category       = "SYSTEM"
)

// Counters records raised and deduplicated alerts.
type Counters interface {
	Raised(alertType string, level entity.AlertLevel)
	Deduplicated(alertType string)
}

// Service raises alerts.
type Service struct {
	alerts   repo.Alerts
	counters Counters
	log      *slog.Logger
}

// New returns a Service.
func New(alerts repo.Alerts, counters Counters, log *slog.Logger) *Service {
	return &Service{alerts: alerts, counters: counters, log: log}
}

func notificationType(level entity.AlertLevel) string {
	switch level {
	case entity.AlertInfo:
		return "INFO"
	case entity.AlertWarn:
		return "WARNING"
	case entity.AlertCritical:
		return "ERROR"
	default:
		return "INFO"
	}
}

// Raise records the alert unless one with the same dedup key (the type by default) is open,
// and notifies the admins; failures are only logged.
func (s *Service) Raise(ctx context.Context, a entity.Alert) {
	if a.DedupKey == "" {
		a.DedupKey = a.Type
	}

	id, created, err := s.alerts.CreateIfNoOpen(ctx, &a)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to record alert", "dedup_key", a.DedupKey, "error", err.Error())

		return
	}

	if !created {
		s.counters.Deduplicated(a.Type)

		return
	}

	s.counters.Raised(a.Type, a.Level)
	s.log.WarnContext(ctx, "alert raised", "level", string(a.Level), "type", a.Type, "message", a.Message)
	s.notifyAdmins(ctx, id, &a)
}

func (s *Service) notifyAdmins(ctx context.Context, id int, a *entity.Alert) {
	admins, err := s.alerts.AdminIDs(ctx, _recipientLimit)
	if err != nil {
		s.log.ErrorContext(ctx, "failed to notify admins of alert", "alert_id", id, "error", err.Error())

		return
	}

	for _, adminID := range admins {
		err := s.alerts.Notify(ctx, &entity.Notification{
			UserID:   adminID,
			Type:     notificationType(a.Level),
			Category: _category,
			Title:    truncateUTF16("["+string(a.Level)+"] "+a.Type, _titleMaxLength),
			Content:  a.Message,
			Metadata: map[string]any{"alertId": id, "dedupKey": a.DedupKey},
		})
		if err != nil {
			s.log.ErrorContext(ctx, "failed to notify admins of alert", "alert_id", id, "error", err.Error())

			return
		}
	}
}

func truncateUTF16(s string, n int) string {
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return s
	}

	return string(utf16.Decode(units[:n]))
}
