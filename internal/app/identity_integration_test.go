//go:build integration

package app_test

import (
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
	"github.com/xingyunqi-tech/go-template/pkg/clientip"
)

const _password = "Passw0rd!"

type identityApp struct {
	*testApp

	smtp *testutil.SMTPServer
}

// newIdentityApp runs the API and a worker against one database, mailing to a test SMTP server;
// rate limits are off unless limits is true, and then X-Forwarded-For names the client.
func newIdentityApp(t *testing.T, limits bool) *identityApp {
	t.Helper()

	redisDB := _redis.Borrow(t)
	smtp := testutil.StartSMTP(t)

	proxies, err := clientip.Parse("1")
	require.NoError(t, err)

	a := newApp(t, func(c *config.Config) {
		c.Redis.URL = config.Secret(redisDB.URL())
		c.Mail.SMTPURL = config.Secret(smtp.URL)
		c.App.FrontendURL = "https://app.example.com/"
		c.HTTP.RateLimitEnabled = limits
		c.HTTP.TrustedProxies = proxies
	}, nil)

	client, err := app.NewWorkerClient(a.cfg, slog.New(slog.DiscardHandler), a.infra)
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NoError(t, client.Start(t.Context()))
	t.Cleanup(func() { _ = client.Stop(t.Context()) })

	return &identityApp{testApp: a, smtp: smtp}
}

type session struct {
	access  string
	refresh string
	user    map[string]any
}

func sessionOf(t *testing.T, r *result) session {
	t.Helper()

	d := r.data()
	require.NotNil(t, d, r.Raw)

	user, _ := d["user"].(map[string]any)
	access, _ := d["accessToken"].(string)
	refresh, _ := d["refreshToken"].(string)

	return session{access: access, refresh: refresh, user: user}
}

func (a *identityApp) register(email string) session {
	a.t.Helper()

	r := a.do(http.MethodPost, "/api/v1/auth/email/register", "", `{"email":"`+email+`","password":"`+_password+`","displayName":"Ada"}`)
	require.Equal(a.t, http.StatusCreated, r.Status, r.Raw)

	return sessionOf(a.t, r)
}

func (a *identityApp) login(email, password string, headers ...string) *result {
	a.t.Helper()

	return a.do(http.MethodPost, "/api/v1/auth/email/login", "", `{"email":"`+email+`","password":"`+password+`"}`, headers...)
}

func (a *identityApp) signIn(email string) session {
	a.t.Helper()

	r := a.login(email, _password)
	require.Equal(a.t, http.StatusOK, r.Status, r.Raw)

	return sessionOf(a.t, r)
}

func (a *identityApp) refresh(token string) *result {
	a.t.Helper()

	return a.do(http.MethodPost, "/api/v1/auth/refresh", "", `{"refreshToken":"`+token+`"}`)
}

func (a *identityApp) exec(sql string) {
	a.t.Helper()

	_, err := a.infra.Pool.Exec(a.t.Context(), sql)
	require.NoError(a.t, err)
}

func adminToken(t *testing.T) string {
	t.Helper()

	return testutil.IssueAccessToken(t, &testutil.TokenOptions{Roles: []string{"admin", "user"}})
}

func TestRegisterVerifyAndSignIn(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)

	s := a.register("Ada@Example.com")
	assert.Equal(t, "ada@example.com", s.user["email"])
	assert.Equal(t, false, s.user["emailVerified"])
	assert.Equal(t, []any{"user"}, s.user["roles"])
	assert.Equal(t, true, s.user["hasPassword"])
	assert.NotEmpty(t, s.access)
	assert.NotEmpty(t, s.refresh)

	mail := a.smtp.Next(t, "ada@example.com")
	assert.Equal(t, "验证你的邮箱", mail.Subject)
	assert.Contains(t, mail.Text, "Ada，你好")
	assert.Contains(t, mail.Text, "https://app.example.com/verify-email?token=")
	assert.Contains(t, mail.HTML, "验证邮箱")

	token := mail.Token(t)

	assert.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/email/verify", "", `{"token":"`+token+`"}`).Status)

	again := a.do(http.MethodPost, "/api/v1/auth/email/verify", "", `{"token":"`+token+`"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, again.Status)
	assert.Equal(t, "auth.verificationTokenInvalid", again.Body["errorKey"])

	signedIn := a.signIn("ADA@example.com")
	assert.Equal(t, true, signedIn.user["emailVerified"])

	me := a.do(http.MethodGet, "/api/v1/me", signedIn.access, "")
	require.Equal(t, http.StatusOK, me.Status)
	assert.Equal(t, "Ada", me.data()["displayName"])

	taken := a.do(http.MethodPost, "/api/v1/auth/email/register", "", `{"email":"ADA@example.com","password":"`+_password+`"}`)
	assert.Equal(t, http.StatusConflict, taken.Status)
	assert.Equal(t, "user.emailTaken", taken.Body["errorKey"])

	for _, r := range []*result{a.login("ada@example.com", "wrong-password"), a.login("nobody@example.com", _password)} {
		assert.Equal(t, http.StatusUnauthorized, r.Status)
		assert.Equal(t, "auth.invalidCredentials", r.Body["errorKey"])
	}

	short := a.do(http.MethodPost, "/api/v1/auth/email/register", "", `{"email":"b@example.com","password":"short"}`)
	require.Equal(t, http.StatusUnprocessableEntity, short.Status)
	assert.Contains(t, short.Raw, `"field":"password","rule":"minLength"`)
}

func TestVerificationMailFollowsTheRequestLanguage(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)

	r := a.do(http.MethodPost, "/api/v1/auth/email/register", "", `{"email":"en@example.com","password":"`+_password+`"}`, "Accept-Language", "en-US")
	require.Equal(t, http.StatusCreated, r.Status)

	first := a.smtp.Next(t, "en@example.com")
	assert.Equal(t, "Verify your email", first.Subject)
	assert.Contains(t, first.Text, "Hi,")

	access := sessionOf(t, r).access

	assert.Equal(t, http.StatusAccepted, a.do(http.MethodPost, "/api/v1/auth/email/verify/resend", access, "", "Accept-Language", "en").Status)

	second := a.smtp.Next(t, "en@example.com")

	stale := a.do(http.MethodPost, "/api/v1/auth/email/verify", "", `{"token":"`+first.Token(t)+`"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, stale.Status)
	assert.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/email/verify", "", `{"token":"`+second.Token(t)+`"}`).Status)
}

func TestRefreshRotatesWithinTheGracePeriodAndRevokesOnReuse(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	first := a.register("rotate@example.com")

	var expiresBefore time.Time

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(), "SELECT expires_at FROM session").Scan(&expiresBefore))

	r := a.refresh(first.refresh)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	second := sessionOf(t, r)
	assert.NotEqual(t, first.refresh, second.refresh)
	assert.Equal(t, "Bearer", r.data()["tokenType"])

	var expiresAfter time.Time

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(), "SELECT expires_at FROM session").Scan(&expiresAfter))
	assert.True(t, expiresBefore.Equal(expiresAfter))

	replayed := a.refresh(first.refresh)
	require.Equal(t, http.StatusOK, replayed.Status, replayed.Raw)
	assert.Equal(t, second.refresh, sessionOf(t, replayed).refresh)
	assert.Equal(t, second.access, sessionOf(t, replayed).access)

	a.exec("UPDATE session_refresh_history SET rotated_at = rotated_at - interval '1 minute'")

	reused := a.refresh(first.refresh)
	assert.Equal(t, http.StatusUnauthorized, reused.Status)
	assert.Equal(t, "auth.refreshTokenReused", reused.Body["errorKey"])

	assert.Equal(t, "auth.refreshTokenInvalid", a.refresh(second.refresh).Body["errorKey"])
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", second.access, "").Status)
}

func TestConcurrentRefreshesShareOnePair(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("race@example.com")

	const n = 8

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		tokens   = map[string]int{}
		statuses []int
	)

	for range n {
		wg.Go(func() {
			r := a.refresh(s.refresh)

			mu.Lock()
			defer mu.Unlock()

			statuses = append(statuses, r.Status)

			if token, ok := r.data()["refreshToken"].(string); ok {
				tokens[token]++
			}
		})
	}

	wg.Wait()

	for _, status := range statuses {
		assert.Equal(t, http.StatusOK, status)
	}

	assert.Len(t, tokens, 1)
}

func TestInvalidRefreshTokens(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("expired@example.com")

	assert.Equal(t, "auth.refreshTokenInvalid", a.refresh("not-a-token").Body["errorKey"])

	a.exec("UPDATE session SET expires_at = now() - interval '1 second'")

	r := a.refresh(s.refresh)
	assert.Equal(t, http.StatusUnauthorized, r.Status)
	assert.Equal(t, "auth.refreshTokenInvalid", r.Body["errorKey"])
}

func TestLogoutAndSessions(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	phone := a.register("devices@example.com")
	laptop := a.signIn("devices@example.com")

	list := a.do(http.MethodGet, "/api/v1/me/sessions", laptop.access, "")
	require.Equal(t, http.StatusOK, list.Status)

	sessions, _ := list.Body["data"].([]any)
	require.Len(t, sessions, 2)

	current := 0

	for _, raw := range sessions {
		if s, _ := raw.(map[string]any); s["current"] == true {
			current++
		}
	}

	assert.Equal(t, 1, current)

	missing := a.do(http.MethodDelete, "/api/v1/me/sessions/"+uuid.NewV7().String(), laptop.access, "")
	assert.Equal(t, http.StatusNotFound, missing.Status)
	assert.Equal(t, "auth.sessionNotFound", missing.Body["errorKey"])

	assert.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/me/sessions/revoke-others", laptop.access, "").Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", phone.access, "").Status)
	assert.Equal(t, "auth.refreshTokenInvalid", a.refresh(phone.refresh).Body["errorKey"])

	assert.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/logout", laptop.access, "").Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", laptop.access, "").Status)
	assert.Equal(t, "auth.refreshTokenInvalid", a.refresh(laptop.refresh).Body["errorKey"])
}

func TestChangeAndResetPassword(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	first := a.register("pw@example.com")
	second := a.signIn("pw@example.com")

	missing := a.do(http.MethodPut, "/api/v1/me/password", first.access, `{"newPassword":"NewPassw0rd!"}`)
	require.Equal(t, http.StatusUnprocessableEntity, missing.Status)
	assert.Contains(t, missing.Raw, `"field":"currentPassword","rule":"required"`)

	wrong := a.do(http.MethodPut, "/api/v1/me/password", first.access, `{"currentPassword":"nope","newPassword":"NewPassw0rd!"}`)
	assert.Equal(t, "auth.passwordIncorrect", wrong.Body["errorKey"])

	changed := a.do(http.MethodPut, "/api/v1/me/password", first.access, `{"currentPassword":"`+_password+`","newPassword":"NewPassw0rd!"}`)
	require.Equal(t, http.StatusOK, changed.Status, changed.Raw)
	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/me", first.access, "").Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", second.access, "").Status)
	assert.Equal(t, http.StatusOK, a.login("pw@example.com", "NewPassw0rd!").Status)

	a.smtp.Next(t, "pw@example.com")

	assert.Equal(t, http.StatusAccepted, a.do(http.MethodPost, "/api/v1/auth/password/forgot", "", `{"email":"nobody@example.com"}`).Status)
	assert.Equal(t, http.StatusAccepted, a.do(http.MethodPost, "/api/v1/auth/password/forgot", "", `{"email":"PW@example.com"}`).Status)

	reset := a.smtp.Next(t, "pw@example.com")
	assert.Equal(t, "重置密码", reset.Subject)
	assert.Contains(t, reset.Text, "https://app.example.com/reset-password?token=")

	done := a.do(http.MethodPost, "/api/v1/auth/password/reset", "", `{"token":"`+reset.Token(t)+`","password":"ResetPassw0rd!"}`)
	require.Equal(t, http.StatusOK, done.Status, done.Raw)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", first.access, "").Status)
	assert.Equal(t, http.StatusOK, a.login("pw@example.com", "ResetPassw0rd!").Status)

	reused := a.do(http.MethodPost, "/api/v1/auth/password/reset", "", `{"token":"`+reset.Token(t)+`","password":"ResetPassw0rd!"}`)
	assert.Equal(t, "auth.verificationTokenInvalid", reused.Body["errorKey"])
}

func TestPasswordlessUserNeedsARecentSignInToSetAPassword(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)

	created := a.do(http.MethodPost, "/api/v1/users", adminToken(t), `{"email":"nopw@example.com"}`)
	require.Equal(t, http.StatusCreated, created.Status, created.Raw)
	assert.Equal(t, false, created.data()["hasPassword"])

	id, _ := created.data()["id"].(string)
	sub := uuid.MustParse(id)

	stale := testutil.IssueAccessToken(t, &testutil.TokenOptions{Subject: sub, AuthTime: time.Now().Add(-time.Hour)})
	r := a.do(http.MethodPut, "/api/v1/me/password", stale, `{"newPassword":"`+_password+`"}`)
	assert.Equal(t, http.StatusForbidden, r.Status)
	assert.Equal(t, "auth.reauthRequired", r.Body["errorKey"])

	fresh := testutil.IssueAccessToken(t, &testutil.TokenOptions{Subject: sub, AuthTime: time.Now()})
	assert.Equal(t, http.StatusOK, a.do(http.MethodPut, "/api/v1/me/password", fresh, `{"newPassword":"`+_password+`"}`).Status)
	assert.Equal(t, http.StatusOK, a.login("nopw@example.com", _password).Status)
}

// from is a distinct client address for the i-th request.
func from(i int) []string {
	return []string{"X-Forwarded-For", "203.0.113." + strconv.Itoa(i+1)}
}

func retryAfter(t *testing.T, r *result) int {
	t.Helper()

	seconds, err := strconv.Atoi(r.Header.Get("Retry-After"))
	require.NoError(t, err, r.Header)

	return seconds
}

func TestFailedSignInsAreLimitedPerAccount(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, true)
	a.register("limited@example.com")

	n := 0
	next := func() []string {
		n++

		return from(n)
	}

	for range 4 {
		assert.Equal(t, http.StatusUnauthorized, a.login("limited@example.com", "wrong-password", next()...).Status)
	}

	assert.Equal(t, http.StatusOK, a.login("limited@example.com", _password, next()...).Status)

	for range 5 {
		assert.Equal(t, http.StatusUnauthorized, a.login("limited@example.com", "wrong-password", next()...).Status)
	}

	blocked := a.login("limited@example.com", _password, next()...)
	require.Equal(t, http.StatusTooManyRequests, blocked.Status)
	assert.Greater(t, retryAfter(t, blocked), 60)

	other := a.login("other@example.com", "wrong-password", next()...)
	assert.Equal(t, http.StatusUnauthorized, other.Status)

	keys, err := a.redisKeys("rl:auth-login-account:a:*")
	require.NoError(t, err)
	assert.Len(t, keys, 2)
}

func TestUserAdministration(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	admin := adminToken(t)
	ada := a.register("ada@example.com")

	forbidden := a.do(http.MethodGet, "/api/v1/users", ada.access, "")
	assert.Equal(t, http.StatusForbidden, forbidden.Status)
	assert.Equal(t, "common.forbidden", forbidden.Body["errorKey"])

	created := a.do(http.MethodPost, "/api/v1/users", admin, `{"phone":"13800000000","displayName":"Bob","password":"`+_password+`"}`)
	require.Equal(t, http.StatusCreated, created.Status, created.Raw)
	assert.Equal(t, "+8613800000000", created.data()["phone"])
	assert.Nil(t, created.data()["email"])

	empty := a.do(http.MethodPost, "/api/v1/users", admin, `{"displayName":"Nobody"}`)
	require.Equal(t, http.StatusUnprocessableEntity, empty.Status)
	assert.Contains(t, empty.Raw, `"field":"email","rule":"required"`)

	taken := a.do(http.MethodPost, "/api/v1/users", admin, `{"phone":"+8613800000000"}`)
	assert.Equal(t, "user.phoneTaken", taken.Body["errorKey"])

	page := a.do(http.MethodGet, "/api/v1/users?keyword=ada&sort=email&page=1&pageSize=10", admin, "")
	require.Equal(t, http.StatusOK, page.Status, page.Raw)
	assert.InDelta(t, 1, page.data()["total"], 0)

	id, _ := ada.user["id"].(string)

	contains := a.do(http.MethodPut, "/api/v1/users/"+id+"/roles", admin, `{"roles":["admin"]}`)
	require.Equal(t, http.StatusUnprocessableEntity, contains.Status)
	assert.Contains(t, contains.Raw, `"field":"roles","rule":"contains"`)

	unknown := a.do(http.MethodPut, "/api/v1/users/"+id+"/roles", admin, `{"roles":["user","owner"]}`)
	assert.Contains(t, unknown.Raw, `"field":"roles[1]","rule":"exists"`)

	promoted := a.do(http.MethodPut, "/api/v1/users/"+id+"/roles", admin, `{"roles":["user","admin","admin"]}`)
	require.Equal(t, http.StatusOK, promoted.Status, promoted.Raw)
	assert.Equal(t, []any{"admin", "user"}, promoted.data()["roles"])
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", ada.access, "").Status)

	admins := a.do(http.MethodGet, "/api/v1/users?role=admin", admin, "")
	assert.InDelta(t, 1, admins.data()["total"], 0)

	last := a.do(http.MethodPatch, "/api/v1/users/"+id, admin, `{"status":"disabled"}`)
	assert.Equal(t, http.StatusConflict, last.Status)
	assert.Equal(t, "role.lastAdmin", last.Body["errorKey"])
	assert.Equal(t, "role.lastAdmin", a.do(http.MethodPut, "/api/v1/users/"+id+"/roles", admin, `{"roles":["user"]}`).Body["errorKey"])

	bobID, _ := created.data()["id"].(string)
	require.Equal(t, http.StatusOK, a.do(http.MethodPut, "/api/v1/users/"+bobID+"/roles", admin, `{"roles":["admin","user"]}`).Status)

	disabled := a.do(http.MethodPatch, "/api/v1/users/"+id, admin, `{"status":"disabled","displayName":"Ada L."}`)
	require.Equal(t, http.StatusOK, disabled.Status, disabled.Raw)
	assert.Equal(t, "disabled", disabled.data()["status"])

	signIn := a.login("ada@example.com", _password)
	assert.Equal(t, http.StatusForbidden, signIn.Status)
	assert.Equal(t, "auth.accountDisabled", signIn.Body["errorKey"])

	assert.Equal(t, "user.notFound", a.do(http.MethodGet, "/api/v1/users/"+uuid.NewV7().String(), admin, "").Body["errorKey"])

	roles := a.do(http.MethodGet, "/api/v1/roles", admin, "")
	require.Equal(t, http.StatusOK, roles.Status)
	assert.Contains(t, roles.Raw, `"code":"admin"`)

	perms := a.do(http.MethodGet, "/api/v1/permissions", admin, "")
	assert.Contains(t, perms.Raw, `{"code":"audit:read"}`)

	var actions []string

	rows, err := a.infra.Pool.Query(t.Context(), "SELECT action FROM audit_log ORDER BY created_at")
	require.NoError(t, err)

	for rows.Next() {
		var action string
		require.NoError(t, rows.Scan(&action))

		actions = append(actions, action)
	}

	assert.Contains(t, actions, "user.create")
	assert.Contains(t, actions, "user.roles.set")
	assert.Contains(t, actions, "user.update")
}

func TestOnlyOneOfTwoConcurrentDisablesOfTheLastAdminsSucceeds(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	admin := adminToken(t)

	var ids []string

	for _, email := range []string{"a1@example.com", "a2@example.com"} {
		s := a.register(email)
		id, _ := s.user["id"].(string)
		require.Equal(t, http.StatusOK, a.do(http.MethodPut, "/api/v1/users/"+id+"/roles", admin, `{"roles":["admin","user"]}`).Status)

		ids = append(ids, id)
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		statuses []int
	)

	for _, id := range ids {
		wg.Go(func() {
			r := a.do(http.MethodPatch, "/api/v1/users/"+id, admin, `{"status":"disabled"}`)

			mu.Lock()
			defer mu.Unlock()

			statuses = append(statuses, r.Status)
		})
	}

	wg.Wait()
	assert.ElementsMatch(t, []int{http.StatusOK, http.StatusConflict}, statuses)
}

func TestJWKSPublishesTheSigningKey(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)

	r := a.get("/.well-known/jwks.json", "")
	require.Equal(t, http.StatusOK, r.Status)
	assert.Equal(t, "max-age=300", r.Header.Get("Cache-Control"))

	var set struct {
		Keys []map[string]any `json:"keys"`
	}

	require.NoError(t, json.Unmarshal([]byte(r.Raw), &set))
	require.Len(t, set.Keys, 1)
	assert.Equal(t, "OKP", set.Keys[0]["kty"])
	assert.Equal(t, "Ed25519", set.Keys[0]["crv"])
	assert.Equal(t, "EdDSA", set.Keys[0]["alg"])
	assert.Equal(t, "sig", set.Keys[0]["use"])

	s := a.register("jwks@example.com")
	assert.Equal(t, set.Keys[0]["kid"], jwsKid(t, s.access))
}

func (a *identityApp) redisKeys(pattern string) ([]string, error) {
	return a.infra.Redis.Keys(a.t.Context(), pattern).Result() //nolint:wrapcheck // test helper
}

func jwsKid(t *testing.T, token string) string {
	t.Helper()

	header, _, _ := strings.Cut(token, ".")

	raw, err := base64.RawURLEncoding.DecodeString(header)
	require.NoError(t, err)

	var h struct {
		Kid string `json:"kid"`
	}

	require.NoError(t, json.Unmarshal(raw, &h))

	return h.Kid
}

func TestSeedCreatesTheDemoAccountsOnce(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	log := slog.New(slog.DiscardHandler)

	require.NoError(t, app.Seed(t.Context(), a.cfg, log, nil))
	require.NoError(t, app.Seed(t.Context(), a.cfg, log, nil))

	admin := a.login("admin@example.com", _password)
	require.Equal(t, http.StatusOK, admin.Status, admin.Raw)

	user, _ := admin.data()["user"].(map[string]any)
	assert.Equal(t, []any{"admin", "user"}, user["roles"])
	assert.Equal(t, true, user["emailVerified"])

	var count int

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(), "SELECT count(*) FROM app_user").Scan(&count))
	assert.Equal(t, 2, count)
}

func TestProductionSeedCreatesAnAdminOnlyWithoutOne(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	log := slog.New(slog.DiscardHandler)

	cfg := *a.cfg
	cfg.App.Env = config.EnvProduction
	cfg.Seed.AdminEmail = "root@example.com"
	cfg.Seed.AdminPassword = config.Secret(_password)

	require.NoError(t, app.Seed(t.Context(), &cfg, log, nil))
	assert.Equal(t, http.StatusOK, a.login("root@example.com", _password).Status)

	cfg.Seed.AdminEmail = "second@example.com"
	require.NoError(t, app.Seed(t.Context(), &cfg, log, nil))
	assert.Equal(t, http.StatusUnauthorized, a.login("second@example.com", _password).Status)
}

func TestCreateSuperuserPromotesAnExistingAccount(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	before := a.register("boss@example.com")
	a.exec("UPDATE app_user SET status = 'disabled'")

	require.NoError(t, app.CreateSuperuser("BOSS@example.com", "NewPassw0rd!")(t.Context(), a.cfg, slog.New(slog.DiscardHandler), nil))

	r := a.login("boss@example.com", "NewPassw0rd!")
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	user, _ := r.data()["user"].(map[string]any)
	assert.Equal(t, []any{"admin", "user"}, user["roles"])
	assert.Equal(t, true, user["emailVerified"])
	assert.Equal(t, "active", user["status"])
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", before.access, "").Status)
}

func TestWrongCurrentPasswordsAreLimitedPerUser(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, true)
	s := a.register("verify@example.com")

	change := func(current string) *result {
		return a.do(http.MethodPut, "/api/v1/me/password", s.access, `{"currentPassword":"`+current+`","newPassword":"`+_password+`"}`)
	}

	for range 4 {
		assert.Equal(t, "auth.passwordIncorrect", change("wrong").Body["errorKey"])
	}

	require.Equal(t, http.StatusOK, change(_password).Status)

	for range 5 {
		assert.Equal(t, "auth.passwordIncorrect", change("wrong").Body["errorKey"])
	}

	blocked := change(_password)
	require.Equal(t, http.StatusTooManyRequests, blocked.Status)
	assert.Greater(t, retryAfter(t, blocked), 60)
}

func TestDisablingAUserSignsItOut(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("disabled@example.com")
	id, _ := s.user["id"].(string)

	require.Equal(t, http.StatusOK, a.do(http.MethodPatch, "/api/v1/users/"+id, adminToken(t), `{"status":"disabled"}`).Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", s.access, "").Status)
	assert.Equal(t, "auth.refreshTokenInvalid", a.refresh(s.refresh).Body["errorKey"])
}

func TestRefreshOfADisabledAccountIsForbidden(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("frozen@example.com")

	a.exec("UPDATE app_user SET status = 'disabled'")

	r := a.refresh(s.refresh)
	assert.Equal(t, http.StatusForbidden, r.Status)
	assert.Equal(t, "auth.accountDisabled", r.Body["errorKey"])
}

func TestRevokingOneSessionKeepsTheOthers(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	phone := a.register("one@example.com")
	laptop := a.signIn("one@example.com")

	var sid string

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(), "SELECT id::text FROM session ORDER BY created_at LIMIT 1").Scan(&sid))

	require.Equal(t, http.StatusOK, a.do(http.MethodDelete, "/api/v1/me/sessions/"+sid, laptop.access, "").Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/me", phone.access, "").Status)
	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, "/api/v1/me", laptop.access, "").Status)

	ttl, err := a.infra.Redis.TTL(t.Context(), "auth:revoked:sid:"+sid).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, a.cfg.Auth.AccessTTL)
	assert.LessOrEqual(t, ttl, a.cfg.Auth.AccessTTL+time.Minute)
}

func TestExpiredOneTimeTokensAreInvalid(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	a.register("late@example.com")

	token := a.smtp.Next(t, "late@example.com").Token(t)

	var verifyTTL time.Duration

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(), "SELECT expires_at - created_at FROM user_token").Scan(&verifyTTL))
	assert.InDelta(t, (24 * time.Hour).Seconds(), verifyTTL.Seconds(), 5)

	a.exec("UPDATE user_token SET expires_at = now() - interval '1 second'")

	r := a.do(http.MethodPost, "/api/v1/auth/email/verify", "", `{"token":"`+token+`"}`)
	assert.Equal(t, "auth.verificationTokenInvalid", r.Body["errorKey"])

	require.Equal(t, http.StatusAccepted, a.do(http.MethodPost, "/api/v1/auth/password/forgot", "", `{"email":"late@example.com"}`).Status)
	a.smtp.Next(t, "late@example.com")

	var resetTTL time.Duration

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(),
		"SELECT expires_at - created_at FROM user_token WHERE purpose = 'password_reset'").Scan(&resetTTL))
	assert.InDelta(t, (30 * time.Minute).Seconds(), resetTTL.Seconds(), 5)
}

func TestUserListKeywordsAndSorts(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	admin := adminToken(t)

	for _, email := range []string{"b_1@example.com", "a1@example.com", "c%1@example.com"} {
		a.register(email)
	}

	emails := func(query string) []string {
		r := a.do(http.MethodGet, "/api/v1/users?"+query, admin, "")
		require.Equal(t, http.StatusOK, r.Status, r.Raw)

		items, _ := r.data()["items"].([]any)
		out := make([]string, 0, len(items))

		for _, it := range items {
			u, _ := it.(map[string]any)
			e, _ := u["email"].(string)
			out = append(out, e)
		}

		return out
	}

	assert.Equal(t, []string{"a1@example.com", "b_1@example.com", "c%1@example.com"}, emails("sort=email,-createdAt"))
	assert.Equal(t, []string{"c%1@example.com"}, emails("keyword="+url.QueryEscape("%")))
	assert.Equal(t, []string{"b_1@example.com"}, emails("keyword=_"))

	dup := a.do(http.MethodGet, "/api/v1/users?sort=email,-email", admin, "")
	require.Equal(t, http.StatusUnprocessableEntity, dup.Status)
	assert.Contains(t, dup.Raw, `"field":"query.sort"`)

	big := a.do(http.MethodGet, "/api/v1/users?page=100001", admin, "")
	assert.Equal(t, http.StatusUnprocessableEntity, big.Status)
}

func TestRoleChangesTouchTheUser(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("touch@example.com")
	id, _ := s.user["id"].(string)

	a.exec("UPDATE app_user SET updated_at = updated_at - interval '1 hour'")

	before := a.do(http.MethodGet, "/api/v1/users/"+id, adminToken(t), "").data()["updatedAt"]
	after := a.do(http.MethodPut, "/api/v1/users/"+id+"/roles", adminToken(t), `{"roles":["admin","user"]}`).data()["updatedAt"]
	assert.NotEqual(t, before, after)
}

func TestUserListBreaksTiesWithTheSecondSortKey(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)

	for _, email := range []string{"b@example.com", "a@example.com", "c@example.com"} {
		a.register(email)
	}

	a.exec("UPDATE app_user SET created_at = '2026-01-01T00:00:00Z'")

	emails := func(sort string) []any {
		r := a.do(http.MethodGet, "/api/v1/users?sort="+sort, adminToken(t), "")
		require.Equal(t, http.StatusOK, r.Status, r.Raw)

		items, _ := r.data()["items"].([]any)
		out := make([]any, 0, len(items))

		for _, it := range items {
			u, _ := it.(map[string]any)
			out = append(out, u["email"])
		}

		return out
	}

	assert.Equal(t, []any{"a@example.com", "b@example.com", "c@example.com"}, emails("-createdAt,email"))
	assert.Equal(t, []any{"c@example.com", "b@example.com", "a@example.com"}, emails("-createdAt,-email"))
}

func TestPasswordChangeIsAuditedForTheCaller(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("change@example.com")

	r := a.do(http.MethodPut, "/api/v1/me/password", s.access, `{"currentPassword":"`+_password+`","newPassword":"NewPassw0rd!"}`)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	id, _ := s.user["id"].(string)

	var actor, resource string

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(),
		"SELECT actor_sub::text, resource_id FROM audit_log WHERE action = 'user.password.change'").Scan(&actor, &resource))
	assert.Equal(t, id, actor)
	assert.Equal(t, id, resource)
}

func TestPasswordResetIsAuditedForTheUser(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("audit@example.com")
	a.smtp.Next(t, "audit@example.com")

	require.Equal(t, http.StatusAccepted, a.do(http.MethodPost, "/api/v1/auth/password/forgot", "", `{"email":"audit@example.com"}`).Status)

	token := a.smtp.Next(t, "audit@example.com").Token(t)
	require.Equal(t, http.StatusOK, a.do(http.MethodPost, "/api/v1/auth/password/reset", "", `{"token":"`+token+`","password":"ResetPassw0rd!"}`).Status)

	id, _ := s.user["id"].(string)

	var actor, resource string

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(),
		"SELECT actor_sub::text, resource_id FROM audit_log WHERE action = 'user.password.reset'").Scan(&actor, &resource))
	assert.Equal(t, id, actor)
	assert.Equal(t, id, resource)
}

func TestConcurrentMailJobsLeaveOneValidToken(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("twice@example.com")
	a.smtp.Next(t, "twice@example.com")

	for range 3 {
		require.Equal(t, http.StatusAccepted, a.do(http.MethodPost, "/api/v1/auth/email/verify/resend", s.access, "").Status)
	}

	for range 3 {
		a.smtp.Next(t, "twice@example.com")
	}

	var valid, retried int

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(),
		"SELECT count(*) FROM user_token WHERE used_at IS NULL AND invalidated_at IS NULL").Scan(&valid))
	assert.Equal(t, 1, valid)

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(),
		"SELECT count(*) FROM river_job WHERE kind = 'mail' AND (attempt > 1 OR coalesce(cardinality(errors), 0) > 0)").Scan(&retried))
	assert.Zero(t, retried)
}
