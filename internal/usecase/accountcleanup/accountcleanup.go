// Package accountcleanup removes the data of deleted accounts: the producer queues one job per
// user, the runner executes every cleaner and the compensation task requeues unfinished users.
package accountcleanup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	// FailedAlert is the alert type of a cleanup that gave up.
	FailedAlert = "accountCleanupFailed"

	_compensationGrace = 30 * time.Minute
	_compensationBatch = 200
	_userCleanerOrder  = 900
)

// Cleaner removes one kind of user data; Clean must be idempotent.
type Cleaner interface {
	Name() string
	Order() int
	Clean(ctx context.Context, userID int) error
}

// Alerter raises admin alerts.
type Alerter interface {
	Raise(ctx context.Context, a entity.Alert)
}

// Error lists the cleaners that failed.
type Error struct {
	UserID int
	Failed []string
	Causes []error
}

func (e *Error) Error() string {
	parts := make([]string, len(e.Failed))
	for i, name := range e.Failed {
		parts[i] = name + " (" + e.Causes[i].Error() + ")"
	}

	return "Account cleanup for user " + strconv.Itoa(e.UserID) + " failed: " + strings.Join(parts, ", ")
}

// Service is the nestjs AccountCleanupProducer, AccountCleanupRunner and compensation task.
type Service struct {
	cleanups repo.AccountCleanups
	jobs     repo.Jobs
	alerts   Alerter
	cleaners []Cleaner
	log      *slog.Logger
	now      func() time.Time
}

// New returns a Service running cleaners in ascending order.
func New(cleanups repo.AccountCleanups, jobs repo.Jobs, alerts Alerter, log *slog.Logger, cleaners ...Cleaner) *Service {
	sorted := slices.Clone(cleaners)
	slices.SortStableFunc(sorted, func(a, b Cleaner) int { return a.Order() - b.Order() })

	return &Service{cleanups: cleanups, jobs: jobs, alerts: alerts, cleaners: sorted, log: log, now: time.Now}
}

// CleanerNames lists the cleaners in execution order.
func (s *Service) CleanerNames() []string {
	names := make([]string, len(s.cleaners))
	for i, c := range s.cleaners {
		names[i] = c.Name()
	}

	return names
}

// Enqueue queues the cleanup of userID; a queueing failure raises an alert instead of failing.
func (s *Service) Enqueue(ctx context.Context, userID int) error {
	if err := s.jobs.EnqueueAccountCleanup(ctx, userID); err != nil {
		s.ReportFailure(ctx, userID, err)
	}

	return nil
}

// Run executes every cleaner; failures do not stop the others and are returned together. The
// cleanup is recorded as completed only when all succeed.
func (s *Service) Run(ctx context.Context, userID int) error {
	failure := &Error{UserID: userID, Failed: nil, Causes: nil}

	for _, c := range s.cleaners {
		if err := c.Clean(ctx, userID); err != nil {
			failure.Failed = append(failure.Failed, c.Name())
			failure.Causes = append(failure.Causes, err)
			s.log.ErrorContext(ctx, "account cleaner failed", "cleaner", c.Name(), "user_id", userID, "error", err.Error())
		}
	}

	if len(failure.Failed) > 0 {
		return failure
	}

	if err := s.cleanups.MarkCompleted(ctx, userID); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	s.log.InfoContext(ctx, "account data cleaned", "user_id", userID)

	return nil
}

// ReportFailure logs and alerts a cleanup that will not be retried.
func (s *Service) ReportFailure(ctx context.Context, userID int, err error) {
	reason := err.Error()
	s.log.ErrorContext(ctx, "account cleanup failed", "user_id", userID, "reason", reason)

	payload := map[string]any{"userId": userID, "reason": reason}

	var failure *Error
	if errors.As(err, &failure) {
		payload["failedCleaners"] = failure.Failed
	}

	s.alerts.Raise(context.WithoutCancel(ctx), entity.Alert{
		Type:     FailedAlert,
		Level:    entity.AlertWarn,
		Message:  fmt.Sprintf("用户 %d 的注销数据清理失败：%s", userID, reason),
		Payload:  payload,
		DedupKey: FailedAlert + ":" + strconv.Itoa(userID),
	})
}

// Compensate requeues up to 200 users removed over 30 minutes ago without a completed cleanup.
func (s *Service) Compensate(ctx context.Context) (int, error) {
	ids, err := s.cleanups.PendingUserIDs(ctx, s.now().Add(-_compensationGrace), _compensationBatch)
	if err != nil {
		return 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	for _, id := range ids {
		if err := s.Enqueue(ctx, id); err != nil {
			return 0, err
		}
	}

	return len(ids), nil
}

// UsersCleaner clears the avatar reference; the avatar file is removed by the files cleaner.
type UsersCleaner struct {
	cleanups repo.AccountCleanups
}

// NewUsersCleaner returns the users cleaner (order 900).
func NewUsersCleaner(cleanups repo.AccountCleanups) *UsersCleaner {
	return &UsersCleaner{cleanups: cleanups}
}

// Name implements Cleaner.
func (*UsersCleaner) Name() string { return "users" }

// Order implements Cleaner.
func (*UsersCleaner) Order() int { return _userCleanerOrder }

// Clean implements Cleaner.
func (c *UsersCleaner) Clean(ctx context.Context, userID int) error {
	return c.cleanups.DetachPhoto(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}
