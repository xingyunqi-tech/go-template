package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	authmocks "github.com/xingyunqi-tech/go-template/internal/usecase/auth/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
	usermocks "github.com/xingyunqi-tech/go-template/internal/usecase/user/mocks"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

const (
	_accessKey  = "access-secret"
	_refreshKey = "refresh-secret"
	_forgotKey  = "forgot-secret"
	_confirmKey = "confirm-secret"
	_expires    = 60_000
	_sessionID  = 7
	_userID     = 5
	_password   = "123456"
	_stored     = "stored-hash"
)

var errStore = errors.New("store down")

type deps struct {
	users    *mocks.MockUsers
	files    *mocks.MockFiles
	sessions *mocks.MockSessions
	cache    *mocks.MockCache
	tx       *mocks.MockTransactions
	bindings *mocks.MockUserBindings
	cleanup  *usermocks.MockAccountCleanup
	hasher   *authmocks.MockVerifier
	mailer   *authmocks.MockMailer
	sms      *authmocks.MockSMSCodes
	settings *authmocks.MockDefaultSettings
	sender   *authmocks.MockSMSSender
	wechat   *authmocks.MockWechat
	alipay   *authmocks.MockAlipay
}

func newService(t *testing.T, mods ...func(*auth.Deps)) (*auth.Service, *deps) {
	t.Helper()

	ctrl := gomock.NewController(t)
	d := &deps{
		users: mocks.NewMockUsers(ctrl), files: mocks.NewMockFiles(ctrl), sessions: mocks.NewMockSessions(ctrl),
		cache: mocks.NewMockCache(ctrl), tx: mocks.NewMockTransactions(ctrl), bindings: mocks.NewMockUserBindings(ctrl),
		cleanup: usermocks.NewMockAccountCleanup(ctrl), hasher: authmocks.NewMockVerifier(ctrl),
		mailer: authmocks.NewMockMailer(ctrl), sms: authmocks.NewMockSMSCodes(ctrl),
		settings: authmocks.NewMockDefaultSettings(ctrl), sender: authmocks.NewMockSMSSender(ctrl),
		wechat: authmocks.NewMockWechat(ctrl), alipay: authmocks.NewMockAlipay(ctrl),
	}
	log := slog.New(slog.DiscardHandler)
	sessions := session.New(d.sessions, d.cache, d.tx, log)
	users := user.New(d.users, d.files, sessions, d.hasher, d.cleanup, log)
	deps := &auth.Deps{
		Config: auth.Config{
			Access:       auth.Secret{Key: _accessKey, ExpiresMillis: _expires},
			Refresh:      auth.Secret{Key: _refreshKey, ExpiresMillis: _expires},
			Forgot:       auth.Secret{Key: _forgotKey, ExpiresMillis: _expires},
			ConfirmEmail: auth.Secret{Key: _confirmKey, ExpiresMillis: _expires},
		},
		Users: users, UserRepo: d.users, Sessions: sessions, SessRepo: d.sessions, Cache: d.cache, Tx: d.tx,
		Hasher: d.hasher, Mailer: d.mailer, SMS: d.sms, Log: log, Settings: d.settings, Sender: d.sender,
		Wechat: d.wechat, Alipay: d.alipay, Bindings: d.bindings,
	}

	for _, mod := range mods {
		mod(deps)
	}

	return auth.New(deps), d
}

func ptr[T any](v T) *T { return &v }

func client() auth.Client {
	return auth.Client{UserAgent: ptr("agent"), IP: ptr("10.0.0.1")}
}

func activeUser() *entity.User {
	return &entity.User{
		ID: _userID, Email: ptr("a@example.com"), Password: ptr(_stored), Provider: entity.ProviderEmail,
		Role:   &entity.Named{ID: entity.RoleUser, Name: "User"},
		Status: &entity.Named{ID: entity.StatusActive, Name: "Active"},
	}
}

func userWithStatus(statusID int) *entity.User {
	u := activeUser()
	u.Status = &entity.Named{ID: statusID, Name: "Other"}

	return u
}

func requireError(t *testing.T, err error, status int, key string, fields ...entity.Detail) {
	t.Helper()

	var e *entity.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, status, e.Status)
	assert.Equal(t, key, e.ErrorKey)

	if len(fields) == 0 {
		fields = nil
	}

	assert.Equal(t, fields, e.Fields)
}

func requireInvalid(t *testing.T, err error, field, key string) {
	t.Helper()
	requireError(t, err, entity.StatusUnprocessable, "", entity.Detail{Name: field, Value: key})
}

func requireUnauthorized(t *testing.T, err error, message string) {
	t.Helper()

	var e *entity.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, entity.StatusUnauthorized, e.Status)
	assert.Equal(t, message, e.Message)
}

func sign(t *testing.T, payload map[string]any, key string, now time.Time) string {
	t.Helper()

	token, err := jwtx.Sign(payload, key, _expires, now)
	require.NoError(t, err)

	return token
}

func claims(t *testing.T, token, key string) map[string]any {
	t.Helper()

	c, err := jwtx.Verify(token, key)
	require.NoError(t, err)

	return c
}

func expectFindByID(d *deps, u *entity.User) {
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(_userID)}).Return(u, nil)
}

func expectFindByEmail(d *deps, email string, u *entity.User) {
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Email: ptr(email)}).Return(u, nil)
}

func expectVerify(d *deps, ok bool, candidates ...any) {
	d.hasher.EXPECT().VerifyAndUpdate(gomock.Any(), _stored, candidates...).Return(ok, "", nil)
}

func expectSession(d *deps) *string {
	var hash string

	userID := _userID

	d.sessions.EXPECT().Create(gomock.Any(), userID, gomock.Any(), ptr("agent"), ptr("10.0.0.1")).
		DoAndReturn(func(_ context.Context, _ int, h string, _, _ *string) (*entity.Session, error) {
			hash = h

			return &entity.Session{ID: _sessionID, Hash: h, UserID: &userID}, nil
		})

	return &hash
}

func expectForget(d *deps, ids ...int) {
	for _, id := range ids {
		d.cache.EXPECT().Delete(gomock.Any(), session.AuthCacheKey(id)).Return(nil).Times(2)
	}

	d.tx.EXPECT().AfterCommit(gomock.Any(), gomock.Any()).Do(func(ctx context.Context, fn func(context.Context)) { fn(ctx) })
}

func expectDeleteAll(d *deps, except *int, ids ...int) {
	d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), _userID, except).Return(ids, nil)
	expectForget(d, ids...)
}

func expectUpdate(d *deps, check func(c *repo.UserChanges)) {
	d.users.EXPECT().Update(gomock.Any(), _userID, gomock.Any()).DoAndReturn(func(_ context.Context, _ int, c *repo.UserChanges) (bool, error) {
		check(c)

		return true, nil
	})
}

func roleOf(t *testing.T, token string) any {
	t.Helper()

	return claims(t, token, _accessKey)["role"]
}

func TestAuthenticateRejectsInvalidTokens(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cases := map[string]string{
		"malformed":      "nope",
		"wrong secret":   sign(t, map[string]any{"id": 5, "sessionId": 7}, "other", now),
		"missing id":     sign(t, map[string]any{"sessionId": 7}, _accessKey, now),
		"zero session":   sign(t, map[string]any{"id": 5, "sessionId": 0}, _accessKey, now),
		"string session": sign(t, map[string]any{"id": 5, "sessionId": "x"}, _accessKey, now),
	}

	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, _ := newService(t)
			_, err := svc.Authenticate(context.Background(), token)
			requireUnauthorized(t, err, "")
		})
	}
}

func accessToken(t *testing.T) string {
	t.Helper()

	return sign(t, map[string]any{"id": _userID, "sessionId": _sessionID}, _accessKey, time.Now())
}

func expectCached(d *deps, body string) {
	d.cache.EXPECT().Get(gomock.Any(), session.AuthCacheKey(_sessionID), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, dst any) (bool, error) {
			return true, json.Unmarshal([]byte(body), dst)
		})
}

func TestAuthenticateUsesCachedSession(t *testing.T) {
	t.Parallel()

	t.Run("active", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCached(d, `{"statusId":1,"roleId":2}`)

		p, err := svc.Authenticate(context.Background(), accessToken(t))
		require.NoError(t, err)
		assert.Equal(t, &auth.Principal{UserID: _userID, SessionID: _sessionID, RoleID: ptr(2)}, p)
	})

	for name, body := range map[string]string{"no status": `{"roleId":2}`, "blocked status": `{"statusId":3,"roleId":2}`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expectCached(d, body)

			_, err := svc.Authenticate(context.Background(), accessToken(t))
			requireUnauthorized(t, err, "User is not active")
		})
	}

	t.Run("cache error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, errStore)

		_, err := svc.Authenticate(context.Background(), accessToken(t))
		require.ErrorIs(t, err, errStore)
	})
}

func TestAuthenticateLoadsAndCachesSession(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.cache.EXPECT().Get(gomock.Any(), session.AuthCacheKey(_sessionID), gomock.Any()).Return(false, nil)
	d.sessions.EXPECT().Authenticate(gomock.Any(), _sessionID).
		Return(&repo.SessionAuth{SessionID: _sessionID, UserID: _userID, StatusID: entity.StatusInactive, RoleID: ptr(2)}, nil)
	d.cache.EXPECT().Set(gomock.Any(), session.AuthCacheKey(_sessionID), gomock.Any(), auth.SessionAuthTTL).
		DoAndReturn(func(_ context.Context, _ string, v any, _ time.Duration) error {
			body, err := json.Marshal(v)
			require.NoError(t, err)
			assert.JSONEq(t, `{"statusId":2,"roleId":2}`, string(body))

			return nil
		})

	p, err := svc.Authenticate(context.Background(), accessToken(t))
	require.NoError(t, err)
	assert.Equal(t, &auth.Principal{UserID: _userID, SessionID: _sessionID, RoleID: ptr(2)}, p)
}

func TestAuthenticateRejectsSessionsFromStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		row     *repo.SessionAuth
		message string
	}{
		{name: "missing", row: nil, message: "Session expired"},
		{name: "other user", row: &repo.SessionAuth{UserID: 6, StatusID: entity.StatusActive}, message: "Session expired"},
		{name: "blocked", row: &repo.SessionAuth{UserID: _userID, StatusID: 3}, message: "User is not active"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			d.cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
			d.sessions.EXPECT().Authenticate(gomock.Any(), _sessionID).Return(tc.row, nil)

			_, err := svc.Authenticate(context.Background(), accessToken(t))
			requireUnauthorized(t, err, tc.message)
		})
	}
}

func TestAuthenticateReturnsStoreErrors(t *testing.T) {
	t.Parallel()

	t.Run("session", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
		d.sessions.EXPECT().Authenticate(gomock.Any(), _sessionID).Return(nil, errStore)

		_, err := svc.Authenticate(context.Background(), accessToken(t))
		require.ErrorIs(t, err, errStore)
	})

	t.Run("cache write", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.cache.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(false, nil)
		d.sessions.EXPECT().Authenticate(gomock.Any(), _sessionID).
			Return(&repo.SessionAuth{UserID: _userID, StatusID: entity.StatusActive}, nil)
		d.cache.EXPECT().Set(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errStore)

		_, err := svc.Authenticate(context.Background(), accessToken(t))
		require.ErrorIs(t, err, errStore)
	})
}

func TestAuthenticateRefresh(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	now := time.Now()

	p, err := svc.AuthenticateRefresh(sign(t, map[string]any{"sessionId": 7, "hash": "h"}, _refreshKey, now))
	require.NoError(t, err)
	assert.Equal(t, &auth.RefreshPrincipal{SessionID: 7, Hash: "h"}, p)

	_, err = svc.AuthenticateRefresh(sign(t, map[string]any{"sessionId": 7}, _accessKey, now))
	requireUnauthorized(t, err, "")

	_, err = svc.AuthenticateRefresh(sign(t, map[string]any{"hash": "h"}, _refreshKey, now))
	requireUnauthorized(t, err, "")
}

func TestLoginWithEmailSignsIn(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectFindByEmail(d, "a@example.com", activeUser())
	d.hasher.EXPECT().VerifyAndUpdate(gomock.Any(), _stored, "abc", "ABC").Return(true, "", nil)

	hash := expectSession(d)

	login, err := svc.LoginWithEmail(context.Background(), "a@example.com", "abc", client())
	require.NoError(t, err)
	assert.Equal(t, _userID, login.User.ID)
	assert.Nil(t, login.Created)

	access := claims(t, login.Tokens.Token, _accessKey)
	assert.InDelta(t, _userID, access["id"], 0)
	assert.InDelta(t, _sessionID, access["sessionId"], 0)
	assert.Equal(t, map[string]any{"id": float64(entity.RoleUser), "name": "User", "__entity": "RoleEntity"}, access["role"])

	refresh := claims(t, login.Tokens.RefreshToken, _refreshKey)
	assert.Equal(t, *hash, refresh["hash"])
	assert.InDelta(t, time.Now().UnixMilli()+_expires, login.Tokens.TokenExpires, 5_000)
}

func TestLoginWithEmailOfUserWithoutRoleSignsNullRole(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	u := activeUser()
	u.Role = nil
	expectFindByEmail(d, "a@example.com", u)
	expectVerify(d, true, _password)
	expectSession(d)

	login, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
	require.NoError(t, err)

	role, present := claims(t, login.Tokens.Token, _accessKey)["role"]
	assert.True(t, present)
	assert.Nil(t, role)
}

func TestLoginWithEmailRehashesOutdatedPassword(t *testing.T) {
	t.Parallel()

	t.Run("rehash", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByEmail(d, "a@example.com", activeUser())
		d.hasher.EXPECT().VerifyAndUpdate(gomock.Any(), _stored, _password).Return(true, "fresh", nil)
		d.users.EXPECT().RehashPassword(gomock.Any(), _userID, _stored, "fresh").Return(nil)
		expectSession(d)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		require.NoError(t, err)
	})

	t.Run("rehash error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByEmail(d, "a@example.com", activeUser())
		d.hasher.EXPECT().VerifyAndUpdate(gomock.Any(), _stored, _password).Return(true, "fresh", nil)
		d.users.EXPECT().RehashPassword(gomock.Any(), _userID, _stored, "fresh").Return(errStore)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		require.ErrorIs(t, err, errStore)
	})
}

func TestLoginWithEmailRejects(t *testing.T) {
	t.Parallel()

	t.Run("unknown email", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByEmail(d, "a@example.com", nil)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		requireInvalid(t, err, "email", "notFound")
	})

	t.Run("wrong password", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByEmail(d, "a@example.com", activeUser())
		expectVerify(d, false, _password)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		requireInvalid(t, err, "password", "incorrectPassword")
	})

	t.Run("user without password", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := activeUser()
		u.Password = nil
		u.Provider = entity.ProviderWechat
		expectFindByEmail(d, "a@example.com", u)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		requireInvalid(t, err, "password", "incorrectPassword")
	})

	t.Run("blocked status", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByEmail(d, "a@example.com", userWithStatus(3))
		expectVerify(d, true, _password)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		requireInvalid(t, err, "email", "userNotActive")
	})
}

func TestLoginWithEmailAllowsInactiveUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectFindByEmail(d, "a@example.com", userWithStatus(entity.StatusInactive))
	expectVerify(d, true, _password)
	expectSession(d)

	_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
	require.NoError(t, err)
}

func TestLoginWithEmailReturnsDependencyErrors(t *testing.T) {
	t.Parallel()

	t.Run("lookup", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("verify", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByEmail(d, "a@example.com", activeUser())
		d.hasher.EXPECT().VerifyAndUpdate(gomock.Any(), _stored, _password).Return(false, "", errStore)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("session", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByEmail(d, "a@example.com", activeUser())
		expectVerify(d, true, _password)
		d.sessions.EXPECT().Create(gomock.Any(), _userID, gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.LoginWithEmail(context.Background(), "a@example.com", _password, client())
		require.ErrorIs(t, err, errStore)
	})
}

func registerInput() *user.Input {
	return &user.Input{Email: repo.Some("a@example.com"), Password: repo.Some(_password)}
}

func expectCreate(d *deps, check func(row *repo.NewUser), created *entity.User) {
	d.users.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, row *repo.NewUser) (int, error) {
		check(row)

		return created.ID, nil
	})
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(created.ID)}).Return(created, nil)
}

func TestRegisterCreatesInactiveUserAndQueuesConfirmation(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.hasher.EXPECT().Hash(gomock.Any(), _password).Return("hashed", nil)
	expectFindByEmail(d, "a@example.com", nil)
	expectCreate(d, func(row *repo.NewUser) {
		assert.Equal(t, ptr(entity.RoleUser), row.RoleID)
		assert.Equal(t, ptr(entity.StatusInactive), row.StatusID)
		assert.Equal(t, ptr("hashed"), row.Password)
	}, userWithStatus(entity.StatusInactive))
	d.settings.EXPECT().InitializeDefaultSettings(gomock.Any(), _userID).Return(errStore)
	d.mailer.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, m auth.Mail) error {
		assert.Equal(t, "activation", m.Template)
		assert.Equal(t, "a@example.com", m.To)

		hash, ok := m.Data["hash"].(string)
		require.True(t, ok)
		assert.InDelta(t, _userID, claims(t, hash, _confirmKey)["confirmEmailUserId"], 0)

		return nil
	})

	require.NoError(t, svc.Register(context.Background(), registerInput()))
}

func TestRegisterWithoutSettingsServiceSkipsInitialization(t *testing.T) {
	t.Parallel()

	svc, d := newService(t, func(deps *auth.Deps) { deps.Settings = nil })
	d.hasher.EXPECT().Hash(gomock.Any(), _password).Return("hashed", nil)
	expectFindByEmail(d, "a@example.com", nil)
	expectCreate(d, func(*repo.NewUser) {}, activeUser())
	d.mailer.EXPECT().Send(gomock.Any(), gomock.Any()).Return(errStore)

	require.ErrorIs(t, svc.Register(context.Background(), registerInput()), errStore)
}

func TestRegisterRejectsTakenEmail(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.hasher.EXPECT().Hash(gomock.Any(), _password).Return("hashed", nil)
	expectFindByEmail(d, "a@example.com", &entity.User{ID: 9})

	requireInvalid(t, svc.Register(context.Background(), registerInput()), "email", "emailAlreadyExists")
}

func confirmHash(t *testing.T, payload map[string]any) string {
	t.Helper()

	return sign(t, payload, _confirmKey, time.Now())
}

func TestConfirmEmailRejectsBadHashes(t *testing.T) {
	t.Parallel()

	expired := sign(t, map[string]any{"confirmEmailUserId": _userID}, _confirmKey, time.Now().Add(-time.Hour))
	cases := map[string]struct {
		hash string
		key  string
	}{
		"malformed":    {hash: "nope", key: "invalidHash"},
		"wrong secret": {hash: sign(t, map[string]any{"confirmEmailUserId": _userID}, _forgotKey, time.Now()), key: "invalidHash"},
		"expired":      {hash: expired, key: "tokenExpired"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, _ := newService(t)
			requireInvalid(t, svc.ConfirmEmail(context.Background(), tc.hash), "hash", tc.key)
			requireInvalid(t, svc.ConfirmNewEmail(context.Background(), tc.hash), "hash", tc.key)
		})
	}
}

func TestConfirmEmail(t *testing.T) {
	t.Parallel()

	hash := confirmHash(t, map[string]any{"confirmEmailUserId": _userID})

	t.Run("activates inactive user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, userWithStatus(entity.StatusInactive))
		expectUpdate(d, func(c *repo.UserChanges) { assert.Equal(t, ptr(entity.StatusActive), c.StatusID) })
		expectFindByID(d, activeUser())

		require.NoError(t, svc.ConfirmEmail(context.Background(), hash))
	})

	t.Run("unknown user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, nil)

		requireError(t, svc.ConfirmEmail(context.Background(), hash), entity.StatusNotFound, "notFound")
	})

	t.Run("already active", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())

		requireInvalid(t, svc.ConfirmEmail(context.Background(), hash), "hash", "alreadyConfirmed")
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		require.ErrorIs(t, svc.ConfirmEmail(context.Background(), hash), errStore)
	})
}

func TestConfirmNewEmail(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		payload map[string]any
		user    *entity.User
		check   func(t *testing.T, c *repo.UserChanges)
	}{
		{
			name: "sets email and activates", payload: map[string]any{"confirmEmailUserId": _userID, "newEmail": "b@example.com"},
			user: userWithStatus(entity.StatusInactive),
			check: func(t *testing.T, c *repo.UserChanges) {
				t.Helper()
				assert.Equal(t, repo.Some("b@example.com"), c.Email)
				assert.Equal(t, ptr(entity.StatusActive), c.StatusID)
			},
		},
		{
			name: "null email clears it", payload: map[string]any{"confirmEmailUserId": _userID, "newEmail": nil},
			user: activeUser(),
			check: func(t *testing.T, c *repo.UserChanges) {
				t.Helper()
				assert.Equal(t, repo.Null[string](), c.Email)
				assert.Nil(t, c.StatusID)
			},
		},
		{
			name: "absent email is left alone", payload: map[string]any{"confirmEmailUserId": _userID},
			user: activeUser(),
			check: func(t *testing.T, c *repo.UserChanges) {
				t.Helper()
				assert.False(t, c.Email.Set)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expectFindByID(d, tc.user)

			if email, ok := tc.payload["newEmail"].(string); ok {
				expectFindByEmail(d, email, nil)
			}

			expectUpdate(d, func(c *repo.UserChanges) { tc.check(t, c) })
			expectFindByID(d, activeUser())

			require.NoError(t, svc.ConfirmNewEmail(context.Background(), confirmHash(t, tc.payload)))
		})
	}
}

func TestConfirmNewEmailOfUnknownUserIsNotFound(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectFindByID(d, nil)

	err := svc.ConfirmNewEmail(context.Background(), confirmHash(t, map[string]any{"confirmEmailUserId": _userID}))
	requireError(t, err, entity.StatusNotFound, "notFound")
}

func TestConfirmNewEmailReturnsLookupError(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

	err := svc.ConfirmNewEmail(context.Background(), confirmHash(t, map[string]any{"confirmEmailUserId": _userID}))
	require.ErrorIs(t, err, errStore)
}

func forgotHash(t *testing.T, svc *auth.Service, d *deps) string {
	t.Helper()

	var hash string

	expectFindByEmail(d, "a@example.com", activeUser())
	d.mailer.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, m auth.Mail) error {
		hash, _ = m.Data["hash"].(string)

		return nil
	})
	require.NoError(t, svc.ForgotPassword(context.Background(), "a@example.com"))

	return hash
}

func TestForgotPasswordQueuesResetMail(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectFindByEmail(d, "a@example.com", activeUser())
	d.mailer.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, m auth.Mail) error {
		assert.Equal(t, "reset-password", m.Template)
		assert.Equal(t, "a@example.com", m.To)
		assert.InDelta(t, time.Now().UnixMilli()+_expires, m.Data["tokenExpires"], 5_000)

		hash, ok := m.Data["hash"].(string)
		require.True(t, ok)

		c := claims(t, hash, _forgotKey)
		assert.InDelta(t, _userID, c["forgotUserId"], 0)
		assert.Regexp(t, `^[0-9a-f]{16}$`, c["pwd"])

		return nil
	})

	require.NoError(t, svc.ForgotPassword(context.Background(), "a@example.com"))
}

func TestForgotPasswordRejectsUnknownEmail(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectFindByEmail(d, "a@example.com", nil)

	requireInvalid(t, svc.ForgotPassword(context.Background(), "a@example.com"), "email", "emailNotExists")
}

func TestForgotPasswordReturnsLookupError(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

	require.ErrorIs(t, svc.ForgotPassword(context.Background(), "a@example.com"), errStore)
}

func TestResetPasswordReplacesPasswordAndSignsOut(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	hash := forgotHash(t, svc, d)

	expectFindByID(d, activeUser())
	d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("fresh", nil)
	d.users.EXPECT().ReplacePassword(gomock.Any(), _userID, ptr(_stored), "fresh").Return(true, nil)
	expectDeleteAll(d, nil, 1, 2)

	require.NoError(t, svc.ResetPassword(context.Background(), hash, "new"))
}

func TestResetPasswordRejects(t *testing.T) {
	t.Parallel()

	t.Run("bad hash", func(t *testing.T) {
		t.Parallel()

		svc, _ := newService(t)
		requireInvalid(t, svc.ResetPassword(context.Background(), "nope", "new"), "hash", "invalidHash")
	})

	t.Run("unknown user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		hash := forgotHash(t, svc, d)
		expectFindByID(d, nil)

		requireInvalid(t, svc.ResetPassword(context.Background(), hash, "new"), "hash", "notFound")
	})

	t.Run("password changed since", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		hash := forgotHash(t, svc, d)
		u := activeUser()
		u.Password = ptr("other")
		expectFindByID(d, u)

		requireInvalid(t, svc.ResetPassword(context.Background(), hash, "new"), "hash", "invalidHash")
	})

	t.Run("concurrent replacement", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		hash := forgotHash(t, svc, d)
		expectFindByID(d, activeUser())
		d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("fresh", nil)
		d.users.EXPECT().ReplacePassword(gomock.Any(), _userID, ptr(_stored), "fresh").Return(false, nil)

		requireInvalid(t, svc.ResetPassword(context.Background(), hash, "new"), "hash", "invalidHash")
	})
}

func TestResetPasswordReturnsDependencyErrors(t *testing.T) {
	t.Parallel()

	t.Run("lookup", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		hash := forgotHash(t, svc, d)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		require.ErrorIs(t, svc.ResetPassword(context.Background(), hash, "new"), errStore)
	})

	t.Run("hash", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		hash := forgotHash(t, svc, d)
		expectFindByID(d, activeUser())
		d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("", errStore)

		require.ErrorIs(t, svc.ResetPassword(context.Background(), hash, "new"), errStore)
	})

	t.Run("replace", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		hash := forgotHash(t, svc, d)
		expectFindByID(d, activeUser())
		d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("fresh", nil)
		d.users.EXPECT().ReplacePassword(gomock.Any(), _userID, gomock.Any(), "fresh").Return(false, errStore)

		require.ErrorIs(t, svc.ResetPassword(context.Background(), hash, "new"), errStore)
	})
}

func principal() *auth.Principal {
	return &auth.Principal{UserID: _userID, SessionID: _sessionID, RoleID: ptr(entity.RoleUser)}
}

func TestMeReturnsCurrentUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	u := activeUser()
	expectFindByID(d, u)

	got, err := svc.Me(context.Background(), principal())
	require.NoError(t, err)
	assert.Same(t, u, got)
}

func TestUpdateMeChangesPasswordAndSignsOutOtherSessions(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	reloaded := activeUser()

	expectFindByID(d, activeUser())
	expectVerify(d, true, _password)
	expectDeleteAll(d, ptr(_sessionID), 3)
	d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("fresh", nil)
	expectUpdate(d, func(c *repo.UserChanges) { assert.Equal(t, repo.Some("fresh"), c.Password) })
	expectFindByID(d, activeUser())
	expectFindByID(d, reloaded)

	got, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Password: repo.Some("new")}, ptr(_password), nil)
	require.NoError(t, err)
	assert.Same(t, reloaded, got)
}

func TestUpdateMeRejectsPasswordChangeWithoutValidOldPassword(t *testing.T) {
	t.Parallel()

	t.Run("missing", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())

		_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Password: repo.Some("new")}, ptr(""), nil)
		requireInvalid(t, err, "oldPassword", "missingOldPassword")
	})

	t.Run("incorrect", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		expectVerify(d, false, _password)

		_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Password: repo.Some("new")}, ptr(_password), nil)
		requireInvalid(t, err, "oldPassword", "incorrectOldPassword")
	})

	t.Run("verify error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		d.hasher.EXPECT().VerifyAndUpdate(gomock.Any(), _stored, _password).Return(false, "", errStore)

		_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Password: repo.Some("new")}, ptr(_password), nil)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("sign out error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		expectVerify(d, true, _password)
		d.sessions.EXPECT().DeleteAllForUser(gomock.Any(), _userID, ptr(_sessionID)).Return(nil, errStore)

		_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Password: repo.Some("new")}, ptr(_password), nil)
		require.ErrorIs(t, err, errStore)
	})
}

func TestUpdateMeTurnsEmailChangeIntoConfirmationMail(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)

	expectFindByID(d, activeUser())
	expectFindByEmail(d, "b@example.com", nil)
	expectVerify(d, true, _password)
	d.mailer.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, m auth.Mail) error {
		assert.Equal(t, "confirm-new-email", m.Template)
		assert.Equal(t, "b@example.com", m.To)

		hash, ok := m.Data["hash"].(string)
		require.True(t, ok)

		c := claims(t, hash, _confirmKey)
		assert.InDelta(t, _userID, c["confirmEmailUserId"], 0)
		assert.Equal(t, "b@example.com", c["newEmail"])

		return nil
	})
	expectUpdate(d, func(c *repo.UserChanges) {
		assert.False(t, c.Email.Set)
		assert.Equal(t, repo.Some("Nick"), c.Nickname)
	})
	expectFindByID(d, activeUser())
	expectFindByID(d, activeUser())

	in := &user.Input{Email: repo.Some("b@example.com"), Nickname: repo.Some("Nick")}
	_, err := svc.UpdateMe(context.Background(), principal(), in, ptr(_password), nil)
	require.NoError(t, err)
}

func TestUpdateMeKeepsUnchangedEmail(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)

	expectFindByID(d, activeUser())
	expectFindByEmail(d, "a@example.com", activeUser())
	expectUpdate(d, func(c *repo.UserChanges) { assert.Equal(t, repo.Some("a@example.com"), c.Email) })
	expectFindByID(d, activeUser())
	expectFindByID(d, activeUser())

	_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Email: repo.Some("a@example.com")}, nil, nil)
	require.NoError(t, err)
}

func TestUpdateMeRejectsEmailOfAnotherUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectFindByID(d, activeUser())
	expectFindByEmail(d, "b@example.com", &entity.User{ID: 9})

	_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Email: repo.Some("b@example.com")}, ptr(_password), nil)
	requireInvalid(t, err, "email", "emailExists")
}

func passwordless(phone *string) *entity.User {
	u := activeUser()
	u.Email = nil
	u.Password = nil
	u.Phone = phone

	return u
}

func TestUpdateMeVerifiesPasswordlessUserBySMS(t *testing.T) {
	t.Parallel()

	in := func() *user.Input { return &user.Input{Email: repo.Some("b@example.com")} }

	t.Run("no phone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, passwordless(nil))
		expectFindByEmail(d, "b@example.com", nil)

		_, err := svc.UpdateMe(context.Background(), principal(), in(), nil, ptr("1234"))
		requireInvalid(t, err, "phone", "phoneRequiredForVerification")
	})

	t.Run("missing code", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, passwordless(ptr("13800000000")))
		expectFindByEmail(d, "b@example.com", nil)

		_, err := svc.UpdateMe(context.Background(), principal(), in(), nil, nil)
		requireInvalid(t, err, "smsCode", "missingSmsCode")
	})

	t.Run("wrong code", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, passwordless(ptr("13800000000")))
		expectFindByEmail(d, "b@example.com", nil)
		d.sms.EXPECT().Verify(gomock.Any(), "13800000000", "1234", "verify_identity").Return(false, nil)

		_, err := svc.UpdateMe(context.Background(), principal(), in(), nil, ptr("1234"))
		requireInvalid(t, err, "code", "invalidCode")
	})

	t.Run("code error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, passwordless(ptr("13800000000")))
		expectFindByEmail(d, "b@example.com", nil)
		d.sms.EXPECT().Verify(gomock.Any(), "13800000000", "1234", "verify_identity").Return(false, errStore)

		_, err := svc.UpdateMe(context.Background(), principal(), in(), nil, ptr("1234"))
		require.ErrorIs(t, err, errStore)
	})

	t.Run("valid code", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, passwordless(ptr("13800000000")))
		expectFindByEmail(d, "b@example.com", nil)
		d.sms.EXPECT().Verify(gomock.Any(), "13800000000", "1234", "verify_identity").Return(true, nil)
		d.mailer.EXPECT().Send(gomock.Any(), gomock.Any()).Return(errStore)

		_, err := svc.UpdateMe(context.Background(), principal(), in(), nil, ptr("1234"))
		require.ErrorIs(t, err, errStore)
	})
}

func TestUpdateMeReturnsLookupErrors(t *testing.T) {
	t.Parallel()

	t.Run("missing user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, nil)

		_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{}, nil, nil)
		requireInvalid(t, err, "user", "userNotFound")
	})

	t.Run("user lookup", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{}, nil, nil)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("email lookup", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Email: ptr("b@example.com")}).Return(nil, errStore)

		_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Email: repo.Some("b@example.com")}, nil, nil)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("update", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		d.users.EXPECT().Update(gomock.Any(), _userID, gomock.Any()).Return(false, errStore)

		_, err := svc.UpdateMe(context.Background(), principal(), &user.Input{Nickname: repo.Some("n")}, nil, nil)
		require.ErrorIs(t, err, errStore)
	})
}

func refreshOld() *auth.RefreshPrincipal {
	return &auth.RefreshPrincipal{SessionID: _sessionID, Hash: "old"}
}

func expectSessionGet(d *deps, s *entity.Session) {
	d.sessions.EXPECT().Get(gomock.Any(), _sessionID).Return(s, nil)
}

func ownedSession(hash string) *entity.Session {
	return &entity.Session{ID: _sessionID, Hash: hash, UserID: ptr(_userID)}
}

func TestRefreshRotatesSessionHash(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectSessionGet(d, ownedSession("old"))
	expectFindByID(d, activeUser())

	var next string

	d.sessions.EXPECT().Rotate(gomock.Any(), _sessionID, "old", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ int, _, n string) (bool, error) {
			next = n

			return true, nil
		})
	d.cache.EXPECT().Set(gomock.Any(), "session:rotated:7", "old", session.RotatedHashGrace).Return(nil)

	tokens, err := svc.Refresh(context.Background(), refreshOld())
	require.NoError(t, err)
	assert.NotEqual(t, "old", next)
	assert.Equal(t, next, claims(t, tokens.RefreshToken, _refreshKey)["hash"])
	assert.Equal(t, map[string]any{"id": float64(entity.RoleUser)}, roleOf(t, tokens.Token))
}

func TestRefreshRejectsUnknownSessionOrOwner(t *testing.T) {
	t.Parallel()

	t.Run("missing session", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, nil)

		_, err := svc.Refresh(context.Background(), refreshOld())
		requireUnauthorized(t, err, "")
	})

	t.Run("session without user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, &entity.Session{ID: _sessionID, Hash: "old"})

		_, err := svc.Refresh(context.Background(), refreshOld())
		requireUnauthorized(t, err, "")
	})

	t.Run("missing user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("old"))
		expectFindByID(d, nil)

		_, err := svc.Refresh(context.Background(), refreshOld())
		requireUnauthorized(t, err, "")
	})

	t.Run("user without role", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := activeUser()
		u.Role = nil

		expectSessionGet(d, ownedSession("old"))
		expectFindByID(d, u)

		_, err := svc.Refresh(context.Background(), refreshOld())
		requireUnauthorized(t, err, "")
	})

	t.Run("blocked user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("old"))
		expectFindByID(d, userWithStatus(3))

		_, err := svc.Refresh(context.Background(), refreshOld())
		requireInvalid(t, err, "refresh", "userNotActive")
	})
}

func TestRefreshWithReplacedHash(t *testing.T) {
	t.Parallel()

	expectRotatedFrom := func(d *deps, hash string) {
		d.cache.EXPECT().Get(gomock.Any(), "session:rotated:7", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, dst any) (bool, error) {
				if s, ok := dst.(*string); ok {
					*s = hash
				}

				return true, nil
			})
	}

	t.Run("within grace window keeps session", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("current"))
		expectFindByID(d, activeUser())
		expectRotatedFrom(d, "old")

		_, err := svc.Refresh(context.Background(), refreshOld())
		requireUnauthorized(t, err, "")
	})

	t.Run("replay revokes session", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("current"))
		expectFindByID(d, activeUser())
		expectRotatedFrom(d, "other")
		d.sessions.EXPECT().Delete(gomock.Any(), _sessionID).Return(nil)
		expectForget(d, _sessionID)

		_, err := svc.Refresh(context.Background(), refreshOld())
		requireUnauthorized(t, err, "")
	})

	t.Run("revoke error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("current"))
		expectFindByID(d, activeUser())
		d.cache.EXPECT().Get(gomock.Any(), "session:rotated:7", gomock.Any()).Return(false, nil)
		d.sessions.EXPECT().Delete(gomock.Any(), _sessionID).Return(errStore)

		_, err := svc.Refresh(context.Background(), refreshOld())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("cache error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("current"))
		expectFindByID(d, activeUser())
		d.cache.EXPECT().Get(gomock.Any(), "session:rotated:7", gomock.Any()).Return(false, errStore)

		_, err := svc.Refresh(context.Background(), refreshOld())
		require.ErrorIs(t, err, errStore)
	})
}

func TestRefreshRotationFailures(t *testing.T) {
	t.Parallel()

	t.Run("lost race", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("old"))
		expectFindByID(d, activeUser())
		d.sessions.EXPECT().Rotate(gomock.Any(), _sessionID, "old", gomock.Any()).Return(false, nil)

		_, err := svc.Refresh(context.Background(), refreshOld())
		requireUnauthorized(t, err, "")
	})

	t.Run("store error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("old"))
		expectFindByID(d, activeUser())
		d.sessions.EXPECT().Rotate(gomock.Any(), _sessionID, "old", gomock.Any()).Return(false, errStore)

		_, err := svc.Refresh(context.Background(), refreshOld())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("session lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.sessions.EXPECT().Get(gomock.Any(), _sessionID).Return(nil, errStore)

		_, err := svc.Refresh(context.Background(), refreshOld())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("user lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectSessionGet(d, ownedSession("old"))
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.Refresh(context.Background(), refreshOld())
		require.ErrorIs(t, err, errStore)
	})
}

func TestDeleteMeVerifiesIdentityAndRemovesUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectFindByID(d, activeUser())
	expectVerify(d, true, _password)
	expectDeleteAll(d, nil)
	d.users.EXPECT().Remove(gomock.Any(), _userID).Return(nil)
	d.cleanup.EXPECT().Enqueue(gomock.Any(), _userID).Return(nil)

	require.NoError(t, svc.DeleteMe(context.Background(), principal(), ptr(_password), nil))
}

func TestDeleteMeRejects(t *testing.T) {
	t.Parallel()

	t.Run("missing user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, nil)

		requireInvalid(t, svc.DeleteMe(context.Background(), principal(), ptr(_password), nil), "user", "userNotFound")
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		require.ErrorIs(t, svc.DeleteMe(context.Background(), principal(), ptr(_password), nil), errStore)
	})

	t.Run("missing old password", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())

		requireInvalid(t, svc.DeleteMe(context.Background(), principal(), nil, nil), "oldPassword", "missingOldPassword")
	})
}

func TestChangePassword(t *testing.T) {
	t.Parallel()

	t.Run("updates and signs out other sessions", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		expectVerify(d, true, _password)
		d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("fresh", nil)
		expectUpdate(d, func(c *repo.UserChanges) { assert.Equal(t, repo.Some("fresh"), c.Password) })
		expectFindByID(d, activeUser())
		expectDeleteAll(d, ptr(_sessionID), 3)

		require.NoError(t, svc.ChangePassword(context.Background(), principal(), _password, "new"))
	})

	t.Run("missing user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, nil)

		requireInvalid(t, svc.ChangePassword(context.Background(), principal(), _password, "new"), "user", "userNotFound")
	})

	t.Run("wrong old password", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		expectVerify(d, false, _password)

		requireInvalid(t, svc.ChangePassword(context.Background(), principal(), _password, "new"), "oldPassword", "incorrectOldPassword")
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		require.ErrorIs(t, svc.ChangePassword(context.Background(), principal(), _password, "new"), errStore)
	})

	t.Run("verify error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		d.hasher.EXPECT().VerifyAndUpdate(gomock.Any(), _stored, _password).Return(false, "", errStore)

		require.ErrorIs(t, svc.ChangePassword(context.Background(), principal(), _password, "new"), errStore)
	})

	t.Run("update error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByID(d, activeUser())
		expectVerify(d, true, _password)
		d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("", errStore)

		require.ErrorIs(t, svc.ChangePassword(context.Background(), principal(), _password, "new"), errStore)
	})
}

func TestSessionManagement(t *testing.T) {
	t.Parallel()

	t.Run("lists active sessions", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		want := []entity.Session{{ID: 1}, {ID: 2}}
		d.sessions.EXPECT().ListActive(gomock.Any(), _userID).Return(want, nil)

		got, err := svc.Sessions(context.Background(), principal())
		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("revokes own session", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.sessions.EXPECT().DeleteForUser(gomock.Any(), 9, _userID).Return(true, nil)
		expectForget(d, 9)

		require.NoError(t, svc.RevokeSession(context.Background(), principal(), 9))
	})

	t.Run("revoking unknown session is not found", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.sessions.EXPECT().DeleteForUser(gomock.Any(), 9, _userID).Return(false, nil)

		requireError(t, svc.RevokeSession(context.Background(), principal(), 9), entity.StatusNotFound, "sessionNotFound")
	})

	t.Run("revoke error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.sessions.EXPECT().DeleteForUser(gomock.Any(), 9, _userID).Return(false, errStore)

		require.ErrorIs(t, svc.RevokeSession(context.Background(), principal(), 9), errStore)
	})

	t.Run("revokes other sessions", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectDeleteAll(d, ptr(_sessionID), 1, 2)

		require.NoError(t, svc.RevokeOtherSessions(context.Background(), principal()))
	})

	t.Run("logout deletes current session", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.sessions.EXPECT().Delete(gomock.Any(), _sessionID).Return(nil)
		expectForget(d, _sessionID)

		require.NoError(t, svc.Logout(context.Background(), principal()))
	})
}
