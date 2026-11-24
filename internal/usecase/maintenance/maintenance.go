// Package maintenance runs the periodic cleanup: expired sessions and spent one-time tokens of
// the issuing service, and audit logs past AUDIT_RETENTION.
package maintenance

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// Pruner deletes the audit logs created before a time.
type Pruner interface {
	Prune(ctx context.Context, before time.Time) (int64, error)
}

// Deps are what the Service uses; a nil Identity skips the identity tables.
type Deps struct {
	Identity  repo.Maintenance
	Audits    Pruner
	Retention time.Duration
	Log       *slog.Logger
	Now       func() time.Time
}

// Service runs the cleanup.
type Service struct {
	Deps
}

// New returns a Service; a nil d.Now is time.Now.
func New(d *Deps) *Service {
	s := &Service{Deps: *d}
	if s.Now == nil {
		s.Now = time.Now
	}

	return s
}

// Run deletes what has expired; every step runs even when an earlier one fails.
func (s *Service) Run(ctx context.Context) error {
	now := s.Now()

	var errs []error

	step := func(name string, fn func() (int64, error)) {
		n, err := fn()
		if err != nil {
			errs = append(errs, err)

			return
		}

		s.Log.InfoContext(ctx, "maintenance", "step", name, "deleted", n)
	}

	if s.Identity != nil {
		step("sessions", func() (int64, error) { return s.Identity.DeleteExpiredSessions(ctx, now) })
		step("tokens", func() (int64, error) { return s.Identity.DeleteSpentTokens(ctx, now) })
	}

	step("audit_logs", func() (int64, error) { return s.Audits.Prune(ctx, now.Add(-s.Retention)) })

	return errors.Join(errs...)
}
