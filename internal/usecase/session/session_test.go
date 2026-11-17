package session_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
)

var errStore = errors.New("store down")

type deps struct {
	sessions *mocks.MockSessions
	cache    *mocks.MockCache
	tx       *mocks.MockTransactions
}

func newService(t *testing.T) (*session.Service, deps) {
	t.Helper()

	ctrl := gomock.NewController(t)
	d := deps{sessions: mocks.NewMockSessions(ctrl), cache: mocks.NewMockCache(ctrl), tx: mocks.NewMockTransactions(ctrl)}

	return session.New(d.sessions, d.cache, d.tx, slog.New(slog.DiscardHandler)), d
}

func runAfterCommit(d deps) {
	d.tx.EXPECT().AfterCommit(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, fn func(context.Context)) { fn(ctx) })
}

func TestNewHashIsRandomHex(t *testing.T) {
	t.Parallel()

	a, err := session.NewHash()
	require.NoError(t, err)
	b, err := session.NewHash()
	require.NoError(t, err)

	assert.Regexp(t, `^[0-9a-f]{64}$`, a)
	assert.NotEqual(t, a, b)
}

func TestReadsDelegateToRepo(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	s := &entity.Session{ID: 5}
	ua := "agent"

	d.sessions.EXPECT().Create(gomock.Any(), 3, "h", &ua, nil).Return(s, nil)
	d.sessions.EXPECT().Get(gomock.Any(), 5).Return(s, nil)
	d.sessions.EXPECT().ListActive(gomock.Any(), 3).Return([]entity.Session{*s}, nil)

	got, err := svc.Create(context.Background(), 3, "h", &ua, nil)
	require.NoError(t, err)
	assert.Same(t, s, got)

	got, err = svc.Get(context.Background(), 5)
	require.NoError(t, err)
	assert.Same(t, s, got)

	list, err := svc.ListActive(context.Background(), 3)
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestRotateRemembersReplacedHash(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.sessions.EXPECT().Rotate(gomock.Any(), 5, "old", "new").Return(true, nil)
	d.cache.EXPECT().Set(gomock.Any(), "session:rotated:5", "old", session.RotatedHashGrace).Return(nil)

	ok, err := svc.Rotate(context.Background(), 5, "old", "new")
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestRotateSkipsCacheWhenHashDoesNotMatch(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.sessions.EXPECT().Rotate(gomock.Any(), 5, "old", "new").Return(false, nil)

	ok, err := svc.Rotate(context.Background(), 5, "old", "new")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRotateReportsCacheFailureAfterRotation(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.sessions.EXPECT().Rotate(gomock.Any(), 5, "old", "new").Return(true, nil)
	d.cache.EXPECT().Set(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errStore)

	ok, err := svc.Rotate(context.Background(), 5, "old", "new")
	require.ErrorIs(t, err, errStore)
	assert.True(t, ok)
}

func TestWasRecentlyRotatedFrom(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		stored string
		found  bool
		err    error
		want   bool
	}{
		{name: "same hash", stored: "old", found: true, want: true},
		{name: "other hash", stored: "older", found: true},
		{name: "absent", found: false},
		{name: "cache error", err: errStore},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			d.cache.EXPECT().Get(gomock.Any(), "session:rotated:5", gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, dst any) (bool, error) {
					if s, ok := dst.(*string); ok {
						*s = tc.stored
					}

					return tc.found, tc.err
				})

			got, err := svc.WasRecentlyRotatedFrom(context.Background(), 5, "old")
			require.ErrorIs(t, err, tc.err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestForgetClearsGuardCacheNowAndAfterCommit(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	runAfterCommit(d)
	d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(1)).Return(nil).Times(2)
	d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(2)).Return(nil).Times(2)

	require.NoError(t, svc.Forget(context.Background(), []int{1, 2}))
}

func TestForgetFailsBeforeRegisteringAfterCommit(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(1)).Return(errStore)

	require.ErrorIs(t, svc.Forget(context.Background(), []int{1}), errStore)
}

func TestForgetAfterCommitFailureIsOnlyLogged(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	runAfterCommit(d)
	gomock.InOrder(
		d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(1)).Return(nil),
		d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(1)).Return(errStore),
	)

	require.NoError(t, svc.Forget(context.Background(), []int{1}))
}

func TestRevokeForUser(t *testing.T) {
	t.Parallel()

	t.Run("existing session is deleted and forgotten", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		runAfterCommit(d)
		d.sessions.EXPECT().DeleteForUser(gomock.Any(), 5, 3).Return(true, nil)
		d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(5)).Return(nil).Times(2)

		ok, err := svc.RevokeForUser(context.Background(), 5, 3)
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("missing session leaves the cache alone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.sessions.EXPECT().DeleteForUser(gomock.Any(), 5, 3).Return(false, nil)

		ok, err := svc.RevokeForUser(context.Background(), 5, 3)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}

func TestDeleteForgetsSession(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	runAfterCommit(d)
	d.sessions.EXPECT().Delete(gomock.Any(), 5).Return(nil)
	d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(5)).Return(nil).Times(2)

	require.NoError(t, svc.Delete(context.Background(), 5))
}

func TestDeleteReturnsRepoError(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.sessions.EXPECT().Delete(gomock.Any(), 5).Return(errStore)

	require.ErrorIs(t, svc.Delete(context.Background(), 5), errStore)
}

func TestDeleteAllForUserForgetsDeletedSessions(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	except := 9

	runAfterCommit(d)
	d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), 3, &except).Return([]int{4}, nil)
	d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(4)).Return(nil).Times(2)

	require.NoError(t, svc.DeleteAllForUser(context.Background(), 3, &except))
}

func TestDeleteAllForUserReturnsRepoError(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), 3, nil).Return(nil, errStore)

	require.ErrorIs(t, svc.DeleteAllForUser(context.Background(), 3, nil), errStore)
}
