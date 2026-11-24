package maintenance_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase/maintenance"
)

type pruner struct {
	before time.Time
	err    error
}

func (p *pruner) Prune(_ context.Context, before time.Time) (int64, error) {
	p.before = before

	return 1, p.err
}

func TestRunCleansTheIdentityTablesAndOldAuditLogs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	identity := mocks.NewMockMaintenance(gomock.NewController(t))
	audits := &pruner{}

	identity.EXPECT().DeleteExpiredSessions(gomock.Any(), now).Return(int64(2), nil)
	identity.EXPECT().DeleteSpentTokens(gomock.Any(), now).Return(int64(0), errors.New("db down"))

	err := maintenance.New(&maintenance.Deps{
		Identity: identity, Audits: audits, Retention: 24 * time.Hour, Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return now },
	}).Run(t.Context())

	require.ErrorContains(t, err, "db down")
	assert.Equal(t, now.Add(-24*time.Hour), audits.before)
}

func TestRunWithoutIdentityTablesOnlyPrunesAuditLogs(t *testing.T) {
	t.Parallel()

	audits := &pruner{}

	require.NoError(t, maintenance.New(&maintenance.Deps{
		Identity: nil, Audits: audits, Retention: time.Hour, Log: slog.New(slog.DiscardHandler), Now: nil,
	}).Run(t.Context()))
	assert.False(t, audits.before.IsZero())
}
