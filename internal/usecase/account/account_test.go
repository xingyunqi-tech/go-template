package account_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/events"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/account"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
)

type noRevoker struct{}

func (noRevoker) Revoke(context.Context, uuid.UUID, time.Duration) error { return nil }

type inline struct{}

func (inline) InTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

type publisher struct {
	events []*events.Event
	err    error
}

func (p *publisher) Publish(_ context.Context, e *events.Event) error {
	p.events = append(p.events, e)

	return p.err
}

type fixture struct {
	svc        *account.Service
	users      *mocks.MockUsers
	identities *mocks.MockIdentities
	sessions   *mocks.MockSessions
	queue      *mocks.MockAccountQueue
	unpurged   *mocks.MockMaintenance
	purger     *mocks.MockPurger
	published  *publisher
	now        time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctrl := gomock.NewController(t)
	f := &fixture{
		users: mocks.NewMockUsers(ctrl), identities: mocks.NewMockIdentities(ctrl), sessions: mocks.NewMockSessions(ctrl),
		queue: mocks.NewMockAccountQueue(ctrl), unpurged: mocks.NewMockMaintenance(ctrl), purger: mocks.NewMockPurger(ctrl), published: &publisher{},
		now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	f.svc = account.New(&account.Deps{
		Users: f.users, Identities: f.identities, Revocation: session.NewRevocation(f.sessions, noRevoker{}, time.Minute),
		Queue: f.queue, Unpurged: f.unpurged, Purgers: []repo.Purger{f.purger}, Publisher: f.published, Tx: inline{},
		Log: slog.New(slog.DiscardHandler), Grace: 7 * 24 * time.Hour, Source: "svc", Now: func() time.Time { return f.now },
	})

	return f
}

func record(id uuid.UUID, roles ...string) *repo.UserRecord {
	return &repo.UserRecord{User: entity.User{ID: id, Status: entity.UserActive, Roles: roles}}
}

func TestDeleteSoftDeletesSignsOutAndSchedulesThePurge(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := uuid.NewV7()

	gomock.InOrder(
		f.users.EXPECT().GetForUpdate(gomock.Any(), id).Return(record(id, "user"), true, nil),
		f.users.EXPECT().SoftDelete(gomock.Any(), id).Return(true, nil),
		f.identities.EXPECT().DeleteAll(gomock.Any(), id).Return(nil),
		f.sessions.EXPECT().RevokeAll(gomock.Any(), id, nil).Return(nil, nil),
		f.queue.EXPECT().EnqueuePurge(gomock.Any(), id, f.now.Add(7*24*time.Hour)).Return(true, nil),
	)

	require.NoError(t, f.svc.DeleteMe(t.Context(), &usecase.Caller{UserID: id, SessionID: uuid.NewV7(), AuthTime: f.now}))
}

func TestDeleteRejectsAMissingUser(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := uuid.NewV7()

	f.users.EXPECT().GetForUpdate(gomock.Any(), id).Return(nil, false, nil)

	require.ErrorIs(t, f.svc.Delete(t.Context(), id), entity.New(entity.KeyUserNotFound))
}

func TestDeleteKeepsTheLastAdmin(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := uuid.NewV7()

	f.users.EXPECT().GetForUpdate(gomock.Any(), id).Return(record(id, "admin", "user"), true, nil)
	f.users.EXPECT().LockAdmins(gomock.Any()).Return(nil)
	f.users.EXPECT().CountActiveAdmins(gomock.Any(), id).Return(0, nil)

	require.ErrorIs(t, f.svc.Delete(t.Context(), id), entity.New(entity.KeyLastAdmin))
}

func TestPurgeRemovesTheUserThenPublishesUserDeleted(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := uuid.NewV7()

	gomock.InOrder(
		f.users.EXPECT().Deleted(gomock.Any(), id).Return(true, true, nil),
		f.purger.EXPECT().Purge(gomock.Any(), id).Return(nil),
		f.users.EXPECT().Purge(gomock.Any(), id).Return(nil),
	)

	require.NoError(t, f.svc.Purge(t.Context(), id))
	require.Len(t, f.published.events, 1)
	assert.Equal(t, events.TypeUserDeleted, f.published.events[0].Type)
	assert.Equal(t, "svc", f.published.events[0].Source)
	assert.JSONEq(t, `{"userId":"`+id.String()+`"}`, string(f.published.events[0].Data))
}

func TestPurgeOfAPurgedUserRunsAgain(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := uuid.NewV7()

	f.users.EXPECT().Deleted(gomock.Any(), id).Return(false, false, nil)
	f.purger.EXPECT().Purge(gomock.Any(), id).Return(nil)
	f.users.EXPECT().Purge(gomock.Any(), id).Return(nil)

	require.NoError(t, f.svc.Purge(t.Context(), id))
	assert.Len(t, f.published.events, 1)
}

func TestPurgeSkipsAUserThatIsNotDeleted(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := uuid.NewV7()

	f.users.EXPECT().Deleted(gomock.Any(), id).Return(false, true, nil)

	require.NoError(t, f.svc.Purge(t.Context(), id))
	assert.Empty(t, f.published.events)
}

func TestPurgeFailuresPublishNothing(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := uuid.NewV7()
	boom := errors.New("boom")

	f.users.EXPECT().Deleted(gomock.Any(), id).Return(true, true, nil)
	f.purger.EXPECT().Purge(gomock.Any(), id).Return(boom)

	require.ErrorIs(t, f.svc.Purge(t.Context(), id), boom)
	assert.Empty(t, f.published.events)
}

func TestPublishFailureFailsThePurge(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	id := uuid.NewV7()
	f.published.err = errors.New("broker down")

	f.users.EXPECT().Deleted(gomock.Any(), id).Return(true, true, nil)
	f.purger.EXPECT().Purge(gomock.Any(), id).Return(nil)
	f.users.EXPECT().Purge(gomock.Any(), id).Return(nil)

	require.ErrorIs(t, f.svc.Purge(t.Context(), id), f.published.err)
}

func TestSweepReschedulesEveryUserPastTheGracePeriodPageByPage(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	before := f.now.Add(-7*24*time.Hour - time.Hour)

	page := make([]repo.UnpurgedUser, 500)
	for i := range page {
		page[i] = repo.UnpurgedUser{ID: uuid.NewV7(), DeletedAt: before.Add(-time.Duration(500-i) * time.Minute)}
	}

	last := uuid.NewV7()

	gomock.InOrder(
		f.unpurged.EXPECT().UnpurgedUsers(gomock.Any(), before, nil, 500).Return(page, nil),
		f.unpurged.EXPECT().UnpurgedUsers(gomock.Any(), before, &page[499], 500).Return([]repo.UnpurgedUser{{ID: last, DeletedAt: before}}, nil),
	)
	f.queue.EXPECT().EnqueuePurge(gomock.Any(), gomock.Any(), f.now).Return(false, nil).Times(500)
	f.queue.EXPECT().EnqueuePurge(gomock.Any(), last, f.now).Return(true, nil)

	require.NoError(t, f.svc.Sweep(t.Context()))
}

func TestAVerifierPurgesOnlyItsOwnDataOnUserDeleted(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	queue, purger := mocks.NewMockAccountQueue(ctrl), mocks.NewMockPurger(ctrl)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc := account.New(&account.Deps{
		Queue: queue, Purgers: []repo.Purger{purger}, Tx: inline{}, Log: slog.New(slog.DiscardHandler),
		Now: func() time.Time { return now },
	})
	id := uuid.NewV7()

	e, err := events.NewUserDeleted("issuer", id, now)
	require.NoError(t, err)

	queue.EXPECT().EnqueuePurge(gomock.Any(), id, now).Return(true, nil)
	require.NoError(t, svc.OnUserDeleted(t.Context(), e))

	for _, e := range []*events.Event{
		{ID: "x", Type: events.TypeUserDeleted, Data: []byte(`"not an object"`)},
		{ID: "y", Type: events.TypeUserDeleted, Data: []byte(`{}`)},
		{ID: "z", Type: "user.created", Data: e.Data},
	} {
		require.NoError(t, svc.OnUserDeleted(t.Context(), e), e.ID)
	}

	queue.EXPECT().EnqueuePurge(gomock.Any(), id, now).Return(false, errors.New("db down"))
	require.Error(t, svc.OnUserDeleted(t.Context(), e), "a failed enqueue is delivered again")

	purger.EXPECT().Purge(gomock.Any(), id).Return(nil)
	require.NoError(t, svc.Purge(t.Context(), id))
	require.NoError(t, svc.Sweep(t.Context()))
}
