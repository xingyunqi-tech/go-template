package auth

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

func TestGraceEntryOpensOnlyWithTheRotatedToken(t *testing.T) {
	t.Parallel()

	p := &pair{Access: "a", Refresh: "r", ExpiresIn: 900}

	sealed, err := seal("old-refresh", p)
	require.NoError(t, err)

	got, err := unseal("old-refresh", sealed)
	require.NoError(t, err)
	assert.Equal(t, p, got)

	_, err = unseal("other-refresh", sealed)
	require.ErrorIs(t, err, errSealed)

	_, err = unseal("old-refresh", sealed[:4])
	require.ErrorIs(t, err, errSealed)
}

func TestRandomTokensAreDistinctAndHashed(t *testing.T) {
	t.Parallel()

	a, hashA, err := randomToken()
	require.NoError(t, err)

	b, _, err := randomToken()
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
	assert.Len(t, a, 43)
	assert.Equal(t, hashToken(a), hashA)
}

type signer struct{}

func (signer) Sign(*jwtx.Claims) (string, error) { return "access", nil }

func TestFailedGraceWriteIsUnavailable(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	users, sessions, grace := mocks.NewMockUsers(ctrl), mocks.NewMockSessions(ctrl), mocks.NewMockRefreshGrace(ctrl)
	now := time.Now()
	rec := &repo.SessionRecord{Session: entity.Session{ID: uuid.NewV7(), UserID: uuid.NewV7(), AuthTime: now, ExpiresAt: now.Add(time.Hour)}}

	sessions.EXPECT().ByRefreshForUpdate(gomock.Any(), hashToken("r")).Return(rec, true, nil)
	users.EXPECT().Get(gomock.Any(), rec.Session.UserID).Return(&repo.UserRecord{User: entity.User{ID: rec.Session.UserID, Status: entity.UserActive}}, true, nil)
	sessions.EXPECT().Rotate(gomock.Any(), rec.Session.ID, hashToken("r"), gomock.Any(), gomock.Any()).Return(nil)
	grace.EXPECT().Put(gomock.Any(), graceKey(hashToken("r")), gomock.Any(), _gracePeriod).Return(errors.New("redis down"))

	s := New(Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, FrontendURL: ""}, &Deps{
		Users: users, Sessions: sessions, Grace: grace, Signer: signer{}, Limits: usecase.Limits{Limiter: nil, Enabled: false},
	})

	_, err := s.Refresh(t.Context(), &usecase.Client{IP: "203.0.113.1"}, "r")
	require.ErrorIs(t, err, entity.New(entity.KeyServiceUnavailable))
}
