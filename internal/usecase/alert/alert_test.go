package alert_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase/alert"
)

type fakeAlerts struct {
	open      map[string]bool
	createErr error
	admins    []int
	notified  []entity.Notification
}

func (f *fakeAlerts) CreateIfNoOpen(_ context.Context, a *entity.Alert) (id int, created bool, err error) {
	if f.createErr != nil {
		return 0, false, f.createErr
	}

	if f.open[a.DedupKey] {
		return 0, false, nil
	}

	f.open[a.DedupKey] = true

	return len(f.open), true, nil
}

func (f *fakeAlerts) AdminIDs(context.Context, int) ([]int, error) { return f.admins, nil }

func (f *fakeAlerts) Notify(_ context.Context, n *entity.Notification) error {
	f.notified = append(f.notified, *n)

	return nil
}

type counters struct {
	raised, deduped []string
}

func (c *counters) Raised(t string, l entity.AlertLevel) {
	c.raised = append(c.raised, t+":"+string(l))
}
func (c *counters) Deduplicated(t string) { c.deduped = append(c.deduped, t) }

func TestRaiseDeduplicatesOpenAlertsAndNotifiesAdmins(t *testing.T) {
	t.Parallel()

	repo := &fakeAlerts{open: map[string]bool{}, admins: []int{1, 7}}
	c := &counters{}
	s := alert.New(repo, c, slog.New(slog.DiscardHandler))

	s.Raise(t.Context(), entity.Alert{Type: "accountCleanupFailed", Level: entity.AlertWarn, Message: "m", Payload: nil, DedupKey: "accountCleanupFailed:3"})
	s.Raise(t.Context(), entity.Alert{Type: "accountCleanupFailed", Level: entity.AlertWarn, Message: "m", Payload: nil, DedupKey: "accountCleanupFailed:3"})
	s.Raise(t.Context(), entity.Alert{Type: "throttler", Level: entity.AlertCritical, Message: "x", Payload: nil, DedupKey: ""})

	assert.Equal(t, []string{"accountCleanupFailed:WARN", "throttler:CRITICAL"}, c.raised)
	assert.Equal(t, []string{"accountCleanupFailed"}, c.deduped)
	assert.True(t, repo.open["throttler"])
	assert.Len(t, repo.notified, 4)
	assert.Equal(t, "[WARN] accountCleanupFailed", repo.notified[0].Title)
	assert.Equal(t, "WARNING", repo.notified[0].Type)
	assert.Equal(t, "SYSTEM", repo.notified[0].Category)
	assert.Equal(t, map[string]any{"alertId": 1, "dedupKey": "accountCleanupFailed:3"}, repo.notified[0].Metadata)
	assert.Equal(t, "ERROR", repo.notified[2].Type)
}

func TestRaiseSwallowsStorageErrorsAndTruncatesTitles(t *testing.T) {
	t.Parallel()

	failing := &fakeAlerts{open: map[string]bool{}, createErr: errors.New("db down")}
	alert.New(failing, &counters{}, slog.New(slog.DiscardHandler)).Raise(t.Context(), entity.Alert{Type: "t", Level: entity.AlertInfo, Message: "", Payload: nil, DedupKey: ""})

	repo := &fakeAlerts{open: map[string]bool{}, admins: []int{1}}
	alert.New(repo, &counters{}, slog.New(slog.DiscardHandler)).Raise(t.Context(), entity.Alert{
		Type: strings.Repeat("长", 300), Level: entity.AlertInfo, Message: "", Payload: nil, DedupKey: "",
	})

	assert.Len(t, []rune(repo.notified[0].Title), 200)
	assert.Equal(t, "INFO", repo.notified[0].Type)
}
