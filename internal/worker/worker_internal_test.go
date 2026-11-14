package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/accountcleanup"
)

func TestDailyScheduleFiresAtTheNextOccurrence(t *testing.T) {
	t.Parallel()

	s := daily{hour: 3, minute: 0, loc: time.UTC}

	assert.Equal(t, time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC), s.Next(time.Date(2026, 11, 14, 2, 59, 59, 0, time.UTC)))
	assert.Equal(t, time.Date(2026, 11, 15, 3, 0, 0, 0, time.UTC), s.Next(time.Date(2026, 11, 14, 3, 0, 0, 0, time.UTC)))
}

func TestHourlyScheduleUsesItsTimeZone(t *testing.T) {
	t.Parallel()

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)

	s := hourly{minute: 25, loc: shanghai}

	next := s.Next(time.Date(2026, 11, 14, 1, 30, 0, 0, time.UTC))
	assert.Equal(t, time.Date(2026, 11, 14, 2, 25, 0, 0, time.UTC), next.UTC())
	assert.Equal(t, time.Date(2026, 11, 14, 1, 25, 0, 0, time.UTC), s.Next(time.Date(2026, 11, 14, 1, 10, 0, 0, time.UTC)).UTC())
}

func TestBackoffDoublesLikeBullMQ(t *testing.T) {
	t.Parallel()

	b := postgres.Backoff{Base: time.Second}
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
		[]time.Duration{b.Delay(1), b.Delay(2), b.Delay(3)})

	w := &AccountCleanupWorker{}
	job := &river.Job[entity.AccountCleanupJob]{JobRow: &rivertype.JobRow{Attempt: 3}}
	assert.WithinDuration(t, time.Now().Add(40*time.Second), w.NextRetry(job), time.Second)
}

type counter struct {
	mu   sync.Mutex
	seen []string
}

func (c *counter) Job(queue, status string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.seen = append(c.seen, queue+":"+status)
}

func TestJobMetricsCountCompletedAndFailedAttempts(t *testing.T) {
	t.Parallel()

	c := &counter{}
	m := &jobMetrics{counter: c}
	row := &rivertype.JobRow{Queue: entity.QueueMail}

	require.NoError(t, m.Work(t.Context(), row, func(context.Context) error { return nil }))
	require.Error(t, m.Work(t.Context(), row, func(context.Context) error { return errors.New("smtp down") }))
	require.Error(t, m.Work(t.Context(), row, func(context.Context) error { return river.JobSnooze(time.Second) }))

	assert.Equal(t, []string{"mail:completed", "mail:failed"}, c.seen)
}

type failingCleaner struct{ calls int }

func (*failingCleaner) Name() string { return "files" }
func (*failingCleaner) Order() int   { return 1000 }
func (f *failingCleaner) Clean(context.Context, int) error {
	f.calls++

	return errors.New("bucket unavailable")
}

type alerts struct {
	mu     sync.Mutex
	raised []entity.Alert
}

func (a *alerts) Raise(_ context.Context, alert entity.Alert) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.raised = append(a.raised, alert)
}

func TestCleanupAlertsOnlyOnTheLastAttempt(t *testing.T) {
	t.Parallel()

	raised := &alerts{}
	cleaner := &failingCleaner{}
	svc := accountcleanup.New(nil, nil, raised, slog.New(slog.DiscardHandler), cleaner)
	w := &AccountCleanupWorker{cleanup: svc, locks: redis.NewLocks(nil, slog.New(slog.DiscardHandler))}

	job := func(attempt int) *river.Job[entity.AccountCleanupJob] {
		return &river.Job[entity.AccountCleanupJob]{
			JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: postgres.AccountCleanupAttempts},
			Args:   entity.AccountCleanupJob{UserID: 9},
		}
	}

	require.Error(t, w.Work(t.Context(), job(4)))
	assert.Empty(t, raised.raised)

	require.Error(t, w.Work(t.Context(), job(5)))
	require.Len(t, raised.raised, 1)

	a := raised.raised[0]
	assert.Equal(t, "accountCleanupFailed", a.Type)
	assert.Equal(t, entity.AlertWarn, a.Level)
	assert.Equal(t, "accountCleanupFailed:9", a.DedupKey)
	assert.Equal(t, []string{"files"}, a.Payload["failedCleaners"])
	assert.Equal(t, "Account cleanup for user 9 failed: files (bucket unavailable)", a.Payload["reason"])
	assert.Equal(t, "用户 9 的注销数据清理失败：Account cleanup for user 9 failed: files (bucket unavailable)", a.Message)
	assert.Equal(t, 2, cleaner.calls)
}

func TestCleanupIsSnoozedWhileAnotherRunHoldsTheLock(t *testing.T) {
	t.Parallel()

	locks := redis.NewLocks(nil, slog.New(slog.DiscardHandler))
	_, ok := locks.TryAcquire(t.Context(), "account-cleanup:user-9", time.Minute)
	require.True(t, ok)

	cleaner := &failingCleaner{}
	w := &AccountCleanupWorker{cleanup: accountcleanup.New(nil, nil, &alerts{}, slog.New(slog.DiscardHandler), cleaner), locks: locks}

	err := w.Work(t.Context(), &river.Job[entity.AccountCleanupJob]{
		JobRow: &rivertype.JobRow{Attempt: 1, MaxAttempts: 5}, Args: entity.AccountCleanupJob{UserID: 9},
	})

	var snooze *river.JobSnoozeError
	require.ErrorAs(t, err, &snooze)
	assert.Zero(t, cleaner.calls)
}
