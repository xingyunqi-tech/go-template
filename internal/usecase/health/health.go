// Package health reports whether the process may receive traffic: live while it runs, ready
// while PostgreSQL and the configured Redis answer and it is not shutting down.
package health

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const _checkTimeout = 2 * time.Second

// Status values of a check.
const (
	StatusOK    = "ok"
	StatusError = "error"
)

// Report is the readiness result: every check and whether all passed.
type Report struct {
	Ready  bool
	Checks map[string]string
}

// Service runs the readiness checks.
type Service struct {
	checks   map[string]repo.Pinger
	draining atomic.Bool
}

// New returns a Service; redis is nil when REDIS_URL is unset.
func New(database, redis repo.Pinger) *Service {
	checks := map[string]repo.Pinger{"database": database}
	if redis != nil {
		checks["redis"] = redis
	}

	return &Service{checks: checks, draining: atomic.Bool{}}
}

// Drain makes Ready fail from now on, while the server shuts down.
func (s *Service) Drain() {
	s.draining.Store(true)
}

// Ready pings every dependency.
func (s *Service) Ready(ctx context.Context) Report {
	report := Report{Ready: !s.draining.Load(), Checks: make(map[string]string, len(s.checks))}

	for name, p := range s.checks {
		checkCtx, cancel := context.WithTimeout(ctx, _checkTimeout)
		err := p.Ping(checkCtx)

		cancel()

		report.Checks[name] = StatusOK
		if err != nil {
			report.Checks[name] = StatusError
			report.Ready = false
		}
	}

	return report
}
