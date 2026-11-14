//go:build integration

package app_test

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // clients send MD5 passwords
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/db"
	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
	"github.com/xingyunqi-tech/go-template/seeds"
)

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // see import

	return hex.EncodeToString(sum[:])
}

var _secret00 = md5Hex("Secret00") //nolint:gochecknoglobals // shared fixture

func loadConfig(t *testing.T, dsn string) *config.Config {
	t.Helper()

	cfg, err := config.Load()
	require.NoError(t, err)

	cfg.Database.URL = dsn

	return cfg
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(t.Context(), dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}

type testApp struct {
	t    *testing.T
	url  string
	cfg  *config.Config
	pool *pgxpool.Pool
}

func newApp(t *testing.T, maxConns int32) *testApp {
	t.Helper()

	cfg := loadConfig(t, _db.Clone(t))

	infra, err := app.OpenInfra(t.Context(), cfg, maxConns)
	require.NoError(t, err)
	t.Cleanup(infra.Close)

	handler, err := app.NewHandler(cfg, slog.New(slog.DiscardHandler), infra)
	require.NoError(t, err)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &testApp{t: t, url: srv.URL, cfg: cfg, pool: infra.Pool}
}

type result struct {
	Status int
	Raw    string
	Body   map[string]any
}

func (r *result) data() map[string]any {
	d, _ := r.Body["data"].(map[string]any)

	return d
}

func (a *testApp) do(method, path, token string, body any, headers ...string) *result {
	a.t.Helper()

	var reader io.Reader = http.NoBody

	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(a.t, err)

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(a.t.Context(), method, a.url+path, reader)
	require.NoError(a.t, err)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(a.t, err)

	out := &result{Status: res.StatusCode, Raw: string(raw), Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)

	return out
}

type tokens struct {
	access, refresh string
}

func (a *testApp) login(email, md5Password string, headers ...string) tokens {
	a.t.Helper()

	r := a.do(http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": email, "password": md5Password}, headers...)
	require.Equal(a.t, http.StatusOK, r.Status, r.Raw)

	token, _ := r.data()["token"].(string)
	refresh, _ := r.data()["refreshToken"].(string)

	return tokens{access: token, refresh: refresh}
}

func TestServeUntilCanceled(t *testing.T) {
	t.Parallel()

	cfg := loadConfig(t, _db.Clone(t))

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.NoError(t, ln.Close())

	cfg.App.Port = addr.Port
	url := "http://127.0.0.1:" + strconv.Itoa(cfg.App.Port) + "/"

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx, cfg, slog.New(slog.DiscardHandler)) }()

	require.Eventually(t, func() bool {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()

		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 20*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestServeFailsWhenPortTaken(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", ":0")
	require.NoError(t, err)

	defer ln.Close()

	cfg := loadConfig(t, _db.Clone(t))

	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)

	cfg.App.Port = addr.Port

	require.Error(t, app.Serve(t.Context(), cfg, slog.New(slog.DiscardHandler)))
}

func TestMigrationsMatchNestjsSchemaSnapshot(t *testing.T) {
	t.Parallel()

	pool := openPool(t, _db.Clone(t))
	tables := []string{
		"user", "role", "status", "session", "permission", "role_permission", "file", "audit_logs", "config", "region",
		"notification", "notification_setting", "address", "favorite", "feedback", "user_consent", "account_cleanup", "admin_alert",
	}

	actual, err := testutil.SchemaSnapshot(t.Context(), pool, tables)
	require.NoError(t, err)

	expected := map[string]bool{}

	for line := range strings.SplitSeq(strings.TrimSpace(db.NestjsSchema), "\n") {
		expected[line] = true
	}

	var extra []string

	for _, line := range actual {
		if !expected[line] {
			extra = append(extra, line)
		}

		delete(expected, line)
	}

	var missing []string
	for line := range expected {
		missing = append(missing, line)
	}

	assert.Empty(t, extra)
	assert.ElementsMatch(t, []string{
		`constraint feedback.FK_feedback_errata FOREIGN KEY ("errataId") REFERENCES errata(id) ON DELETE SET NULL`,
		`constraint feedback.FK_feedback_question FOREIGN KEY ("questionId") REFERENCES question(id) ON DELETE SET NULL`,
	}, missing)
}

func TestMigrateUpIsIdempotentAndSchemaCheckPasses(t *testing.T) {
	t.Parallel()

	pool := openPool(t, _db.Clone(t))

	require.NoError(t, app.MigrateUp(t.Context(), pool, slog.New(slog.DiscardHandler)))
	require.NoError(t, app.CheckSchema(t.Context(), pool))

	_, err := pool.Exec(t.Context(), `ALTER TABLE "user" DROP COLUMN "nickname"`)
	require.NoError(t, err)

	err = app.CheckSchema(t.Context(), pool)
	require.ErrorIs(t, err, app.ErrSchemaMismatch)
	assert.Contains(t, err.Error(), "user.nickname")
}

func TestSeedIsIdempotent(t *testing.T) {
	t.Parallel()

	pool := openPool(t, _db.Clone(t))
	count := func(table string) int {
		var n int
		require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM "`+table+`"`).Scan(&n))

		return n
	}

	before := map[string]int{}
	for _, table := range []string{"user", "role", "status", "permission", "role_permission", "region", "config"} {
		before[table] = count(table)
		assert.Positive(t, before[table], table)
	}

	err := postgres.NewDB(pool, time.Second, slog.New(slog.DiscardHandler)).Seed(t.Context(), seeds.Data, seeds.Regions,
		postgres.SeedOptions{Production: false, AdminEmail: "", AdminPassword: "", Hash: func(context.Context, string) (string, error) {
			return "unused", nil
		}})
	require.NoError(t, err)

	for table, n := range before {
		assert.Equal(t, n, count(table), table)
	}

	assert.Equal(t, 2, before["user"])
}

func TestUniqueViolationIsTranslated(t *testing.T) {
	t.Parallel()

	pool := openPool(t, _db.Clone(t))
	users := postgres.NewUsers(postgres.NewDB(pool, time.Second, slog.New(slog.DiscardHandler)))
	email := "john.doe@example.com"

	_, err := users.Create(t.Context(), &repo.NewUser{Email: &email, Provider: "email"}) //nolint:exhaustruct // other columns default
	require.Error(t, err)

	body := httperr.Build(err, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, body.Status)

	s, _ := json.Marshal(body.Data)
	assert.JSONEq(t, `{"email":"emailAlreadyExists"}`, string(s))
}

func TestLoginResponseShape(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	r := a.do(http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": "john.doe@example.com", "password": _secret00})

	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"refreshToken":"[^"]+","token":"[^"]+","tokenExpires":\d+,"user":\{"id":2,`, r.Raw)
	assert.Contains(t, r.Raw, `"role":{"id":2,"name":"User","__entity":"RoleEntity"},"status":{"id":1,"name":"Active","__entity":"StatusEntity"}`)
	assert.Contains(t, r.Raw, `"gender":0,"birthday":null,"photo":null`)
	assert.NotContains(t, r.Raw, "password")

	token, _ := r.data()["token"].(string)
	claims, err := jwtx.Verify(token, "secret")
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"id": 2.0, "name": "User", "__entity": "RoleEntity"}, claims["role"])

	exp, _ := jwtx.Int(claims, "exp")
	iat, _ := jwtx.Int(claims, "iat")
	assert.Equal(t, 900, exp-iat)
}

func TestLoginErrors(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	r := a.do(http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": "nobody@example.com", "password": _secret00})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"email":"Not found"}}`, r.Raw)

	r = a.do(http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": "john.doe@example.com", "password": md5Hex("wrong")})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"password":"Incorrect password"}}`, r.Raw)

	r = a.do(http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": "JOHN.DOE@example.com", "password": strings.ToUpper(_secret00)})
	assert.Equal(t, http.StatusOK, r.Status, r.Raw)
}

func TestRefreshReplayRevokesSession(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	login := a.login("john.doe@example.com", _secret00)

	refresh := func(token string) *result {
		return a.do(http.MethodPost, "/api/v1/auth/refresh", token, nil)
	}

	r1 := refresh(login.refresh)
	require.Equal(t, http.StatusOK, r1.Status, r1.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"token":"[^"]+","refreshToken":"[^"]+","tokenExpires":\d+\}\}$`, r1.Raw)

	h1, _ := r1.data()["refreshToken"].(string)
	access1, _ := r1.data()["token"].(string)
	claims, err := jwtx.Verify(access1, "secret")
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"id": 2.0}, claims["role"])

	r2 := refresh(h1)
	require.Equal(t, http.StatusOK, r2.Status, r2.Raw)
	h2, _ := r2.data()["refreshToken"].(string)

	assert.Equal(t, http.StatusUnauthorized, refresh(login.refresh).Status)
	assert.Equal(t, http.StatusUnauthorized, refresh(h2).Status)

	me := a.do(http.MethodGet, "/api/v1/auth/me", login.access, nil)
	assert.JSONEq(t, `{"code":401,"message":"Session expired","data":null}`, me.Raw)
}

func TestRefreshWithinGraceWindowKeepsSession(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	login := a.login("john.doe@example.com", _secret00)

	r1 := a.do(http.MethodPost, "/api/v1/auth/refresh", login.refresh, nil)
	require.Equal(t, http.StatusOK, r1.Status)

	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodPost, "/api/v1/auth/refresh", login.refresh, nil).Status)

	h1, _ := r1.data()["refreshToken"].(string)
	assert.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/refresh", h1, nil).Status)
}

func TestRegisterConfirmAndLogin(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	body := map[string]any{"email": "New.User@Example.com", "password": _secret00, "firstName": "<b>New</b>", "lastName": "User"}

	r := a.do(http.MethodPost, "/api/v1/auth/email/register", "", body)
	require.JSONEq(t, `{"code":200,"message":"success"}`, r.Raw)

	r = a.do(http.MethodPost, "/api/v1/auth/email/register", "", body)
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"email":"Email already exists"}}`, r.Raw)

	login := a.login("new.user@example.com", _secret00)
	me := a.do(http.MethodGet, "/api/v1/auth/me", login.access, nil)
	assert.Equal(t, "New", me.data()["firstName"])
	assert.Equal(t, map[string]any{"id": 2.0, "name": "Inactive", "__entity": "StatusEntity"}, me.data()["status"])

	id, _ := me.data()["id"].(float64)
	hash, err := jwtx.Sign(map[string]any{"confirmEmailUserId": int(id)}, "secret_for_confirm_email", 60_000, time.Now())
	require.NoError(t, err)

	r = a.do(http.MethodPost, "/api/v1/auth/email/confirm", "", map[string]any{"hash": hash})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	r = a.do(http.MethodPost, "/api/v1/auth/email/confirm", "", map[string]any{"hash": hash})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"hash":"Email is already confirmed"}}`, r.Raw)

	r = a.do(http.MethodPost, "/api/v1/auth/email/confirm", "", map[string]any{"hash": "nope"})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"hash":"Invalid verification link"}}`, r.Raw)

	expired, err := jwtx.Sign(map[string]any{"confirmEmailUserId": int(id)}, "secret_for_confirm_email", 1000, time.Now().Add(-time.Hour))
	require.NoError(t, err)

	r = a.do(http.MethodPost, "/api/v1/auth/email/confirm", "", map[string]any{"hash": expired})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"hash":"Verification link has expired"}}`, r.Raw)
}

func TestForgotAndResetPassword(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	other := a.login("john.doe@example.com", _secret00)

	r := a.do(http.MethodPost, "/api/v1/auth/forgot/password", "", map[string]any{"email": "nobody@example.com"})
	assert.Equal(t, http.StatusUnprocessableEntity, r.Status)
	assert.Contains(t, r.Raw, `"data":{"email":`)

	require.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/forgot/password", "", map[string]any{"email": "john.doe@example.com"}).Status)

	var stored string
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "password" FROM "user" WHERE "id" = 2`).Scan(&stored))

	sum := sha256Hex(stored)
	hash, err := jwtx.Sign(map[string]any{"forgotUserId": 2, "pwd": sum[:16]}, "secret_for_forgot", 60_000, time.Now())
	require.NoError(t, err)

	newPassword := md5Hex("Changed00")
	r = a.do(http.MethodPost, "/api/v1/auth/reset/password", "", map[string]any{"hash": hash, "password": newPassword})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	r = a.do(http.MethodPost, "/api/v1/auth/reset/password", "", map[string]any{"hash": hash, "password": newPassword})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"hash":"Invalid verification link"}}`, r.Raw)

	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/auth/me", other.access, nil).Status)
	a.login("john.doe@example.com", newPassword)
}

func TestSessionsAreListedAndRevoked(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	first := a.login("john.doe@example.com", _secret00, "User-Agent", "Device-A")
	second := a.login("john.doe@example.com", _secret00, "User-Agent", "Device-B")
	third := a.login("john.doe@example.com", _secret00, "User-Agent", "Device-C")

	list := a.do(http.MethodGet, "/api/v1/auth/sessions", first.access, nil)
	require.Equal(t, http.StatusOK, list.Status)

	sessions, _ := list.Body["data"].([]any)
	require.Len(t, sessions, 3)

	var secondID float64

	for _, s := range sessions {
		m, _ := s.(map[string]any)
		if m["userAgent"] == "Device-B" {
			secondID, _ = m["id"].(float64)
		}

		assert.Equal(t, m["userAgent"] == "Device-A", m["current"])
	}

	path := "/api/v1/auth/sessions/" + strconv.Itoa(int(secondID))
	require.Equal(t, http.StatusOK, a.do(http.MethodDelete, path, first.access, nil).Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/auth/me", second.access, nil).Status)
	assert.JSONEq(t, `{"code":404,"message":"Session not found","data":null}`, a.do(http.MethodDelete, path, first.access, nil).Raw)

	require.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/sessions/revoke-others", first.access, nil).Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/auth/me", third.access, nil).Status)
	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/auth/me", first.access, nil).Status)

	invalid := a.do(http.MethodDelete, "/api/v1/auth/sessions/abc", first.access, nil)
	assert.JSONEq(t, `{"code":400,"message":"Bad request","data":{"id":"Invalid id"}}`, invalid.Raw)
}

func TestLogoutInvalidatesCachedSession(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	login := a.login("john.doe@example.com", _secret00)

	require.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/auth/me", login.access, nil).Status)
	require.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/logout", login.access, nil).Status)
	assert.JSONEq(t, `{"code":401,"message":"Session expired","data":null}`, a.do(http.MethodGet, "/api/v1/auth/me", login.access, nil).Raw)
}

func TestAuthenticatedWritesNeedOneConnection(t *testing.T) {
	t.Parallel()

	a := newApp(t, 1)
	login := a.login("john.doe@example.com", _secret00)

	done := make(chan int, 1)

	go func() { done <- a.do(http.MethodPost, "/api/v1/auth/sessions/revoke-others", login.access, nil).Status }()

	select {
	case status := <-done:
		assert.Equal(t, http.StatusOK, status)
	case <-time.After(15 * time.Second):
		t.Fatal("request waited for a second connection")
	}
}

func TestUpdateProfileAndChangePassword(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	login := a.login("john.doe@example.com", _secret00)
	other := a.login("john.doe@example.com", _secret00)

	r := a.do(http.MethodPut, "/api/v1/auth/me", login.access, map[string]any{"firstName": "Jane", "nickname": nil, "gender": 2, "birthday": "1990-02-03"})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Equal(t, "Jane", r.data()["firstName"])
	assert.Equal(t, "1990-02-03", r.data()["birthday"])
	assert.InDelta(t, 2, r.data()["gender"], 0)

	r = a.do(http.MethodPut, "/api/v1/auth/me", login.access, map[string]any{"password": md5Hex("Next0000")})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"oldPassword":"Old password is required"}}`, r.Raw)

	r = a.do(http.MethodPut, "/api/v1/auth/password", login.access, map[string]any{"oldPassword": md5Hex("bad"), "newPassword": md5Hex("Next0000")})
	assert.Equal(t, http.StatusUnprocessableEntity, r.Status)

	r = a.do(http.MethodPut, "/api/v1/auth/password", login.access, map[string]any{"oldPassword": _secret00, "newPassword": md5Hex("Next0000")})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/auth/me", other.access, nil).Status)
	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/auth/me", login.access, nil).Status)
	a.login("john.doe@example.com", md5Hex("Next0000"))
}

func TestDeleteAccountReleasesEmail(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	login := a.login("john.doe@example.com", _secret00)

	r := a.do(http.MethodDelete, "/api/v1/auth/me", login.access, map[string]any{"oldPassword": md5Hex("bad")})
	assert.Equal(t, http.StatusUnprocessableEntity, r.Status)

	r = a.do(http.MethodDelete, "/api/v1/auth/me", login.access, map[string]any{"oldPassword": _secret00})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/auth/me", login.access, nil).Status)
	r = a.do(http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": "john.doe@example.com", "password": _secret00})
	assert.Equal(t, http.StatusUnprocessableEntity, r.Status)

	r = a.do(http.MethodPost, "/api/v1/auth/email/register", "", map[string]any{"email": "john.doe@example.com", "password": _secret00, "firstName": "J", "lastName": "D"})
	assert.Equal(t, http.StatusOK, r.Status, r.Raw)
}

func TestUsersAdministration(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)
	john := a.login("john.doe@example.com", _secret00)

	assert.JSONEq(t, `{"code":403,"message":"Missing permissions: user:list","data":null}`, a.do(http.MethodGet, "/api/v1/users", john.access, nil).Raw)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/users", "", nil).Status)

	created := a.do(http.MethodPost, "/api/v1/users", admin.access, map[string]any{
		"email": "Created@Example.com", "password": _secret00, "firstName": "C", "lastName": "U",
		"role": map[string]any{"id": 2}, "status": map[string]any{"id": 1},
	})
	require.Equal(t, http.StatusOK, created.Status, created.Raw)
	assert.Contains(t, created.Raw, `"photo":null,"role":{"id":2},"status":{"id":1},"createdAt"`)

	id, _ := created.data()["id"].(float64)
	path := "/api/v1/users/" + strconv.Itoa(int(id))

	got := a.do(http.MethodGet, path, admin.access, nil)
	assert.Equal(t, "created@example.com", got.data()["email"])
	assert.JSONEq(t, `{"code":200,"message":"success","data":null}`, a.do(http.MethodGet, "/api/v1/users/9999", admin.access, nil).Raw)

	bad := a.do(http.MethodPost, "/api/v1/users", admin.access, map[string]any{"email": "x@example.com", "role": map[string]any{"id": 9}})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"role":"Role does not exist"}}`, bad.Raw)

	updated := a.do(http.MethodPut, path, admin.access, map[string]any{"lastName": "Updated", "status": map[string]any{"id": 2}})
	require.Equal(t, http.StatusOK, updated.Status, updated.Raw)
	assert.Equal(t, "Updated", updated.data()["lastName"])

	list := a.do(http.MethodGet, `/api/v1/users?limit=2&filters={"roles":[{"id":2}]}&sort=[{"orderBy":"email","order":"desc"}]`, admin.access, nil)
	require.Equal(t, http.StatusOK, list.Status, list.Raw)

	page, _ := list.Body["data"].(map[string]any)
	users, _ := page["data"].([]any)
	require.Len(t, users, 2)
	assert.Equal(t, true, page["hasNextPage"])

	first, _ := users[0].(map[string]any)
	assert.Equal(t, "john.doe@example.com", first["email"])

	unknown := a.do(http.MethodGet, `/api/v1/users?sort=[{"orderBy":"nope","order":"asc"}]`, admin.access, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, unknown.Status)
	assert.Contains(t, unknown.Raw, `"sort":{"0":{"orderBy":`)

	require.Equal(t, http.StatusOK, a.do(http.MethodDelete, path, admin.access, nil).Status)
	assert.JSONEq(t, `{"code":200,"message":"success","data":null}`, a.do(http.MethodGet, path, admin.access, nil).Raw)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))

	return hex.EncodeToString(sum[:])
}

func TestConcurrentRefreshRotatesOnce(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	login := a.login("john.doe@example.com", _secret00)

	const attempts = 5

	results := make(chan *result, attempts)

	for range attempts {
		go func() { results <- a.do(http.MethodPost, "/api/v1/auth/refresh", login.refresh, nil) }()
	}

	var winners []*result

	for range attempts {
		r := <-results
		switch r.Status {
		case http.StatusOK:
			winners = append(winners, r)
		case http.StatusUnauthorized:
		default:
			t.Fatalf("unexpected status %d: %s", r.Status, r.Raw)
		}
	}

	require.Len(t, winners, 1)

	next, _ := winners[0].data()["refreshToken"].(string)
	assert.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/refresh", next, nil).Status)
}

func TestLastActiveAtIsUpdatedAtMostEveryFiveMinutes(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	stale := a.login("john.doe@example.com", _secret00)
	fresh := a.login("john.doe@example.com", _secret00)

	sessionOf := func(token string) int {
		claims, err := jwtx.Verify(token, "secret")
		require.NoError(t, err)

		id, _ := jwtx.Int(claims, "sessionId")

		return id
	}
	setAge := func(id int, age string) {
		_, err := a.pool.Exec(t.Context(), `UPDATE "session" SET "lastActiveAt" = now() - $2::interval WHERE "id" = $1`, id, age)
		require.NoError(t, err)
	}
	ageSeconds := func(id int) float64 {
		var age float64
		require.NoError(t, a.pool.QueryRow(t.Context(),
			`SELECT extract(epoch FROM now() - "lastActiveAt") FROM "session" WHERE "id" = $1`, id).Scan(&age))

		return age
	}

	setAge(sessionOf(stale.access), "10 minutes")
	setAge(sessionOf(fresh.access), "2 minutes")

	require.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/auth/me", stale.access, nil).Status)
	require.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/auth/me", fresh.access, nil).Status)

	assert.Less(t, ageSeconds(sessionOf(stale.access)), 60.0)
	assert.InDelta(t, 120, ageSeconds(sessionOf(fresh.access)), 30)
}

func TestConfirmNewEmail(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	confirm := func(claims map[string]any) *result {
		hash, err := jwtx.Sign(claims, "secret_for_confirm_email", 60_000, time.Now())
		require.NoError(t, err)

		return a.do(http.MethodPost, "/api/v1/auth/email/confirm/new", "", map[string]any{"hash": hash})
	}
	email := func() *string {
		var e *string
		require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "email" FROM "user" WHERE "id" = 2`).Scan(&e))

		return e
	}

	require.Equal(t, http.StatusOK, confirm(map[string]any{"confirmEmailUserId": 2}).Status)
	require.NotNil(t, email())
	assert.Equal(t, "john.doe@example.com", *email())

	require.Equal(t, http.StatusOK, confirm(map[string]any{"confirmEmailUserId": 2, "newEmail": "john.new@example.com"}).Status)
	assert.Equal(t, "john.new@example.com", *email())

	r := confirm(map[string]any{"confirmEmailUserId": 9999, "newEmail": "x@example.com"})
	assert.JSONEq(t, `{"code":404,"message":"Not found","data":null}`, r.Raw)
}

func TestEmptyProfileUpdateKeepsUpdatedAt(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	login := a.login("john.doe@example.com", _secret00)

	before := a.do(http.MethodGet, "/api/v1/auth/me", login.access, nil).data()["updatedAt"]
	after := a.do(http.MethodPut, "/api/v1/auth/me", login.access, map[string]any{}).data()["updatedAt"]
	assert.Equal(t, before, after)
}
