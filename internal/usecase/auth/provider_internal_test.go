package auth

import (
	"context"
	"log/slog"
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
)

type provider struct {
	id       string
	identity *entity.ExternalIdentity
	err      error
}

func (p provider) ID() string { return p.id }

func (p provider) Exchange(context.Context, string) (*entity.ExternalIdentity, error) {
	return p.identity, p.err
}

type inline struct{}

func (inline) InTx(ctx context.Context, fn func(ctx context.Context) error) error { return fn(ctx) }

type codes struct{ err error }

func (c codes) Verify(context.Context, string, string, string) error { return c.err }

type providerFixture struct {
	svc        *Service
	users      *mocks.MockUsers
	identities *mocks.MockIdentities
	sessions   *mocks.MockSessions
}

func newProviderFixture(t *testing.T, verify error, providers ...Provider) *providerFixture {
	t.Helper()

	ctrl := gomock.NewController(t)
	f := &providerFixture{users: mocks.NewMockUsers(ctrl), identities: mocks.NewMockIdentities(ctrl), sessions: mocks.NewMockSessions(ctrl)}
	f.svc = New(Config{AccessTTL: time.Minute, RefreshTTL: time.Hour, FrontendURL: ""}, &Deps{
		Log: slog.New(slog.DiscardHandler), Users: f.users, Identities: f.identities, Sessions: f.sessions, Signer: signer{}, Codes: codes{err: verify},
		Providers: providers, Limits: usecase.Limits{Limiter: nil, Enabled: false}, Tx: inline{},
	})

	return f
}

func active(id uuid.UUID) *repo.UserRecord {
	return &repo.UserRecord{User: entity.User{ID: id, Status: entity.UserActive, Roles: []string{"user"}}}
}

func withPhone(id uuid.UUID, phone string) *repo.UserRecord {
	rec := active(id)
	rec.User.Phone = &phone

	return rec
}

func TestProvidersFollowTheConventionOrder(t *testing.T) {
	t.Parallel()

	f := newProviderFixture(t, nil, provider{id: "alipay"}, provider{id: "wechat_miniapp"})

	assert.Equal(t, []string{"wechat_miniapp", "alipay"}, f.svc.Providers())
}

func TestUnknownProviderIsDisabled(t *testing.T) {
	t.Parallel()

	f := newProviderFixture(t, nil)

	_, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "wechat_app", "c")
	require.ErrorIs(t, err, entity.New(entity.KeyProviderDisabled))
}

func TestProviderErrorsPassThrough(t *testing.T) {
	t.Parallel()

	f := newProviderFixture(t, nil, provider{id: "alipay", err: entity.New(entity.KeyProviderCodeInvalid)})

	_, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "alipay", "c")
	require.ErrorIs(t, err, entity.New(entity.KeyProviderCodeInvalid))
}

func TestKnownIdentityGetsItsUnionIDFilledIn(t *testing.T) {
	t.Parallel()

	id := &entity.ExternalIdentity{Provider: "wechat_app", Subject: "o1", UnionID: "u1"}
	f := newProviderFixture(t, nil, provider{id: "wechat_app", identity: id})
	userID := uuid.NewV7()

	gomock.InOrder(
		f.identities.EXPECT().Lock(gomock.Any(), "union:u1").Return(nil),
		f.identities.EXPECT().Lock(gomock.Any(), "wechat_app:o1").Return(nil),
		f.identities.EXPECT().Find(gomock.Any(), "wechat_app", "o1").Return(userID, true, true, nil),
		f.users.EXPECT().GetForUpdate(gomock.Any(), userID).Return(active(userID), true, nil),
		f.identities.EXPECT().FindByUnion(gomock.Any(), "u1", entity.WechatProviders()).Return(uuid.Nil(), false, nil),
		f.identities.EXPECT().SetUnion(gomock.Any(), "wechat_app", "o1", "u1").Return(nil),
	)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	r, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "wechat_app", "c")
	require.NoError(t, err)
	assert.Equal(t, userID, r.User.ID)
}

func TestNewWechatIdentityJoinsTheAccountOfItsUnionID(t *testing.T) {
	t.Parallel()

	id := &entity.ExternalIdentity{Provider: "wechat_miniapp", Subject: "o2", UnionID: "u1"}
	f := newProviderFixture(t, nil, provider{id: "wechat_miniapp", identity: id})
	userID := uuid.NewV7()

	f.identities.EXPECT().Lock(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	f.identities.EXPECT().Find(gomock.Any(), "wechat_miniapp", "o2").Return(uuid.Nil(), false, false, nil)
	gomock.InOrder(
		f.identities.EXPECT().FindByUnion(gomock.Any(), "u1", entity.WechatProviders()).Return(userID, true, nil),
		f.users.EXPECT().GetForUpdate(gomock.Any(), userID).Return(active(userID), true, nil),
		f.identities.EXPECT().Create(gomock.Any(), userID, id).Return(nil),
	)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	_, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "wechat_miniapp", "c")
	require.NoError(t, err)
}

func TestUnionIDOfADeletedAccountStartsANewAccount(t *testing.T) {
	t.Parallel()

	id := &entity.ExternalIdentity{Provider: "wechat_miniapp", Subject: "o2", UnionID: "u1"}
	f := newProviderFixture(t, nil, provider{id: "wechat_miniapp", identity: id})
	deleted, fresh := uuid.NewV7(), uuid.NewV7()

	f.identities.EXPECT().Lock(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	gomock.InOrder(
		f.identities.EXPECT().Find(gomock.Any(), "wechat_miniapp", "o2").Return(uuid.Nil(), false, false, nil),
		f.identities.EXPECT().FindByUnion(gomock.Any(), "u1", entity.WechatProviders()).Return(deleted, true, nil),
		f.users.EXPECT().GetForUpdate(gomock.Any(), deleted).Return(nil, false, nil),
		f.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(&entity.User{ID: fresh, Status: entity.UserActive}, nil),
		f.identities.EXPECT().Create(gomock.Any(), fresh, id).Return(nil),
	)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	r, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "wechat_miniapp", "c")
	require.NoError(t, err)
	assert.Equal(t, fresh, r.User.ID)
}

func TestFirstAlipaySignInCreatesAnAccount(t *testing.T) {
	t.Parallel()

	id := &entity.ExternalIdentity{Provider: "alipay", Subject: "2088", UnionID: ""}
	f := newProviderFixture(t, nil, provider{id: "alipay", identity: id})
	userID := uuid.NewV7()

	f.identities.EXPECT().Lock(gomock.Any(), "alipay:2088").Return(nil)
	f.identities.EXPECT().Find(gomock.Any(), "alipay", "2088").Return(uuid.Nil(), false, false, nil)
	f.users.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u *repo.NewUser) (*entity.User, error) {
		assert.Nil(t, u.Email)
		assert.Nil(t, u.Phone)
		assert.Equal(t, []string{"user"}, u.Roles)

		return &entity.User{ID: userID, Status: entity.UserActive, Roles: u.Roles}, nil
	})
	f.identities.EXPECT().Create(gomock.Any(), userID, id).Return(nil)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	r, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "alipay", "c")
	require.NoError(t, err)
	assert.Equal(t, userID, r.User.ID)
}

func TestDisabledProviderAccountCannotSignIn(t *testing.T) {
	t.Parallel()

	id := &entity.ExternalIdentity{Provider: "alipay", Subject: "2088", UnionID: ""}
	f := newProviderFixture(t, nil, provider{id: "alipay", identity: id})
	userID := uuid.NewV7()

	f.identities.EXPECT().Lock(gomock.Any(), "alipay:2088").Return(nil)
	f.identities.EXPECT().Find(gomock.Any(), "alipay", "2088").Return(userID, false, true, nil)
	f.users.EXPECT().GetForUpdate(gomock.Any(), userID).Return(&repo.UserRecord{User: entity.User{ID: userID, Status: entity.UserDisabled}}, true, nil)

	_, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "alipay", "c")
	require.ErrorIs(t, err, entity.New(entity.KeyAccountDisabled))
}

func TestSmsSignInRegistersANewPhone(t *testing.T) {
	t.Parallel()

	f := newProviderFixture(t, nil)
	userID := uuid.NewV7()

	f.users.EXPECT().GetByPhone(gomock.Any(), "+8613800138000").Return(nil, false, nil)
	f.users.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, u *repo.NewUser) (*entity.User, error) {
		assert.Equal(t, "+8613800138000", *u.Phone)

		return &entity.User{ID: userID}, nil
	})
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	r, err := f.svc.LoginWithSMS(t.Context(), &usecase.Client{}, "13800138000", "123456")
	require.NoError(t, err)
	assert.Equal(t, userID, r.User.ID)
}

func TestSmsSignInRetriesWhenTheSameNewPhoneSignsInConcurrently(t *testing.T) {
	t.Parallel()

	f := newProviderFixture(t, nil)
	userID := uuid.NewV7()

	gomock.InOrder(
		f.users.EXPECT().GetByPhone(gomock.Any(), "+8613800138000").Return(nil, false, nil),
		f.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, entity.New(entity.KeyPhoneTaken)),
		f.users.EXPECT().GetByPhone(gomock.Any(), "+8613800138000").Return(withPhone(userID, "+8613800138000"), true, nil),
		f.users.EXPECT().GetForUpdate(gomock.Any(), userID).Return(withPhone(userID, "+8613800138000"), true, nil),
	)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	_, err := f.svc.LoginWithSMS(t.Context(), &usecase.Client{}, "+8613800138000", "123456")
	require.NoError(t, err)
}

func TestSmsSignInWithAWrongCode(t *testing.T) {
	t.Parallel()

	f := newProviderFixture(t, entity.New(entity.KeyVerificationCodeInvalid))

	_, err := f.svc.LoginWithSMS(t.Context(), &usecase.Client{}, "+8613800138000", "123456")
	require.ErrorIs(t, err, entity.New(entity.KeyVerificationCodeInvalid))

	_, err = f.svc.LoginWithSMS(t.Context(), &usecase.Client{}, "12", "123456")
	require.ErrorAs(t, err, new(*entity.ValidationError))
}

func TestUnionIDOfAnotherAccountIsNotFilledIn(t *testing.T) {
	t.Parallel()

	id := &entity.ExternalIdentity{Provider: "wechat_miniapp", Subject: "o1", UnionID: "u1"}
	f := newProviderFixture(t, nil, provider{id: "wechat_miniapp", identity: id})
	userID, other := uuid.NewV7(), uuid.NewV7()

	f.identities.EXPECT().Lock(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	f.identities.EXPECT().Find(gomock.Any(), "wechat_miniapp", "o1").Return(userID, true, true, nil)
	f.identities.EXPECT().FindByUnion(gomock.Any(), "u1", entity.WechatProviders()).Return(other, true, nil)
	f.users.EXPECT().GetForUpdate(gomock.Any(), userID).Return(active(userID), true, nil)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	r, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "wechat_miniapp", "c")
	require.NoError(t, err)
	assert.Equal(t, userID, r.User.ID)
}

func TestIdentityOfAJustDeletedAccountIsMatchedAgain(t *testing.T) {
	t.Parallel()

	id := &entity.ExternalIdentity{Provider: "alipay", Subject: "2088", UnionID: ""}
	f := newProviderFixture(t, nil, provider{id: "alipay", identity: id})
	deleted, fresh := uuid.NewV7(), uuid.NewV7()

	f.identities.EXPECT().Lock(gomock.Any(), "alipay:2088").Return(nil)
	gomock.InOrder(
		f.identities.EXPECT().Find(gomock.Any(), "alipay", "2088").Return(deleted, false, true, nil),
		f.users.EXPECT().GetForUpdate(gomock.Any(), deleted).Return(nil, false, nil),
		f.identities.EXPECT().Find(gomock.Any(), "alipay", "2088").Return(uuid.Nil(), false, false, nil),
		f.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(&entity.User{ID: fresh, Status: entity.UserActive}, nil),
		f.identities.EXPECT().Create(gomock.Any(), fresh, id).Return(nil),
	)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	r, err := f.svc.LoginWithProvider(t.Context(), &usecase.Client{}, "alipay", "c")
	require.NoError(t, err)
	assert.Equal(t, fresh, r.User.ID)
}

func TestSmsSignInOfAJustDeletedUserRegistersThePhoneAgain(t *testing.T) {
	t.Parallel()

	f := newProviderFixture(t, nil)
	deleted, fresh := uuid.NewV7(), uuid.NewV7()

	gomock.InOrder(
		f.users.EXPECT().GetByPhone(gomock.Any(), "+8613800138000").Return(active(deleted), true, nil),
		f.users.EXPECT().GetForUpdate(gomock.Any(), deleted).Return(nil, false, nil),
		f.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(&entity.User{ID: fresh}, nil),
	)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	r, err := f.svc.LoginWithSMS(t.Context(), &usecase.Client{}, "+8613800138000", "123456")
	require.NoError(t, err)
	assert.Equal(t, fresh, r.User.ID)
}

func TestSmsSignInOfAUserWhoRebindsThePhoneRegistersItAgain(t *testing.T) {
	t.Parallel()

	f := newProviderFixture(t, nil)
	moved, fresh := uuid.NewV7(), uuid.NewV7()

	gomock.InOrder(
		f.users.EXPECT().GetByPhone(gomock.Any(), "+8613800138000").Return(withPhone(moved, "+8613800138000"), true, nil),
		f.users.EXPECT().GetForUpdate(gomock.Any(), moved).Return(withPhone(moved, "+8613900139000"), true, nil),
		f.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(&entity.User{ID: fresh, Status: entity.UserActive}, nil),
	)
	f.sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)

	r, err := f.svc.LoginWithSMS(t.Context(), &usecase.Client{}, "+8613800138000", "123456")
	require.NoError(t, err)
	assert.Equal(t, fresh, r.User.ID)
}
