package session_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
)

type revoker struct {
	sids []uuid.UUID
	ttl  time.Duration
	err  error
}

func (r *revoker) Revoke(_ context.Context, sid uuid.UUID, ttl time.Duration) error {
	r.sids = append(r.sids, sid)
	r.ttl = ttl

	return r.err
}

func TestListFlagsTheCurrentSession(t *testing.T) {
	t.Parallel()

	sessions := mocks.NewMockSessions(gomock.NewController(t))
	caller := &usecase.Caller{UserID: uuid.NewV7(), SessionID: uuid.NewV7(), AuthTime: time.Now()}

	sessions.EXPECT().ListActive(gomock.Any(), caller.UserID).Return([]entity.Session{{ID: uuid.NewV7()}, {ID: caller.SessionID}}, nil)

	views, err := session.New(sessions, session.NewRevocation(sessions, &revoker{}, time.Minute)).List(t.Context(), caller)
	require.NoError(t, err)
	require.Len(t, views, 2)
	assert.False(t, views[0].Current)
	assert.True(t, views[1].Current)
}

func TestRevokeWritesTheKeyForTheAccessLifetimeAndAMinute(t *testing.T) {
	t.Parallel()

	sessions := mocks.NewMockSessions(gomock.NewController(t))
	keys := &revoker{}
	svc := session.New(sessions, session.NewRevocation(sessions, keys, 15*time.Minute))
	caller := &usecase.Caller{UserID: uuid.NewV7(), SessionID: uuid.NewV7(), AuthTime: time.Now()}
	other := uuid.NewV7()

	sessions.EXPECT().Revoke(gomock.Any(), caller.UserID, other).Return(true, nil)
	require.NoError(t, svc.Revoke(t.Context(), caller, other))
	assert.Equal(t, []uuid.UUID{other}, keys.sids)
	assert.Equal(t, 16*time.Minute, keys.ttl)

	sessions.EXPECT().Revoke(gomock.Any(), caller.UserID, other).Return(false, nil)
	require.ErrorIs(t, svc.Revoke(t.Context(), caller, other), entity.New(entity.KeySessionNotFound))
}

func TestRevokeOthersKeepsTheCallerAndReportsRedisFailures(t *testing.T) {
	t.Parallel()

	sessions := mocks.NewMockSessions(gomock.NewController(t))
	keys := &revoker{err: errors.New("redis down")}
	caller := &usecase.Caller{UserID: uuid.NewV7(), SessionID: uuid.NewV7(), AuthTime: time.Now()}

	sessions.EXPECT().RevokeAll(gomock.Any(), caller.UserID, &caller.SessionID).Return([]uuid.UUID{uuid.NewV7()}, nil)

	err := session.New(sessions, session.NewRevocation(sessions, keys, time.Minute)).RevokeOthers(t.Context(), caller)
	require.ErrorIs(t, err, entity.New(entity.KeyServiceUnavailable))
}
