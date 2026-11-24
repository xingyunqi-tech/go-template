//go:build integration

package app_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
)

const _rsaBits = 2048

// startWechat fakes the WeChat API: a code "<openid>.<unionid>" signs in, "bad" is rejected,
// "down" fails with HTTP 500, and a phone code "phone.<number>" resolves to +86<number>.
func startWechat(t *testing.T) string {
	t.Helper()

	reply := func(w http.ResponseWriter, v map[string]any) { _ = json.NewEncoder(w).Encode(v) }

	exchange := func(w http.ResponseWriter, code string) {
		switch code {
		case "bad":
			reply(w, map[string]any{"errcode": 40029, "errmsg": "invalid code"})
		case "down":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			openid, unionid, _ := strings.Cut(code, ".")
			reply(w, map[string]any{"openid": openid, "unionid": unionid})
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sns/jscode2session":
			exchange(w, r.URL.Query().Get("js_code"))
		case "/sns/oauth2/access_token":
			exchange(w, r.URL.Query().Get("code"))
		case "/cgi-bin/stable_token":
			reply(w, map[string]any{"access_token": "t", "expires_in": 7200})
		case "/wxa/business/getuserphonenumber":
			var body map[string]string

			_ = json.NewDecoder(r.Body).Decode(&body)

			number, ok := strings.CutPrefix(body["code"], "phone.")
			if !ok {
				reply(w, map[string]any{"errcode": 40029, "errmsg": "invalid code"})

				return
			}

			reply(w, map[string]any{"errcode": 0, "phone_info": map[string]any{"purePhoneNumber": number, "countryCode": "86"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

// alipayGateway fakes alipay.system.oauth.token: the auth code is the user id, "bad" is rejected
// and "forged" is signed with another key.
type alipayGateway struct {
	url        string
	appKey     string
	publicKey  string
	gate       *rsa.PrivateKey
	forgingKey *rsa.PrivateKey
}

func startAlipay(t *testing.T) *alipayGateway {
	t.Helper()

	newKey := func() *rsa.PrivateKey {
		k, err := rsa.GenerateKey(rand.Reader, _rsaBits)
		require.NoError(t, err)

		return k
	}

	app, g := newKey(), &alipayGateway{gate: newKey(), forgingKey: newKey()}

	appDER, err := x509.MarshalPKCS8PrivateKey(app)
	require.NoError(t, err)

	pubDER, err := x509.MarshalPKIXPublicKey(&g.gate.PublicKey)
	require.NoError(t, err)

	g.appKey, g.publicKey = base64.StdEncoding.EncodeToString(appDER), base64.StdEncoding.EncodeToString(pubDER)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()

		code := r.PostForm.Get("code")
		if code == "bad" {
			_, _ = w.Write([]byte(`{"error_response":{"code":"40002","sub_code":"isv.code-invalid"}}`))

			return
		}

		key := g.gate
		if code == "forged" {
			key = g.forgingKey
		}

		node := `{"user_id":"` + code + `","access_token":"t"}`
		sum := sha256.Sum256([]byte(node))

		sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		_, _ = w.Write([]byte(`{"alipay_system_oauth_token_response":` + node + `,"sign":"` + base64.StdEncoding.EncodeToString(sig) + `"}`)) //nolint:gosec // fake gateway of the test
	}))
	t.Cleanup(srv.Close)

	g.url = srv.URL

	return g
}

func newProviderApp(t *testing.T) *identityApp {
	t.Helper()

	wechat, ali := startWechat(t), startAlipay(t)

	return newIdentityApp(t, false, func(c *config.Config) {
		c.Wechat.APIBase = wechat
		c.Wechat.MiniAppID, c.Wechat.MiniAppSecret = "wxmini", "s1"
		c.Wechat.AppID, c.Wechat.AppSecret = "wxapp", "s2"
		c.Alipay.AppID, c.Alipay.PrivateKey, c.Alipay.PublicKey, c.Alipay.Gateway = "2021", config.Secret(ali.appKey), ali.publicKey, ali.url
	})
}

func (a *identityApp) providerLogin(provider, code string) *result {
	a.t.Helper()

	return a.do(http.MethodPost, "/api/v1/auth/providers/"+provider+"/login", "", `{"code":"`+code+`"}`)
}

func (a *identityApp) providerSignIn(provider, code string) session {
	a.t.Helper()

	r := a.providerLogin(provider, code)
	require.Equal(a.t, http.StatusOK, r.Status, r.Raw)

	return sessionOf(a.t, r)
}

// unionID is the union_id of a wechat_miniapp identity.
func (a *identityApp) unionID(subject string) *string {
	a.t.Helper()

	var union *string

	require.NoError(a.t, a.infra.Pool.QueryRow(a.t.Context(),
		`SELECT union_id FROM user_identity WHERE provider = 'wechat_miniapp' AND subject = $1`, subject).Scan(&union))

	return union
}

func TestListAuthProviders(t *testing.T) {
	t.Parallel()

	r := newIdentityApp(t, false).get("/api/v1/auth/providers", "")
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Equal(t, []any{}, r.Body["data"])

	r = newProviderApp(t).get("/api/v1/auth/providers", "")
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Equal(t, []any{
		map[string]any{"id": "wechat_miniapp"}, map[string]any{"id": "wechat_app"}, map[string]any{"id": "alipay"},
	}, r.Body["data"])
}

func TestWechatProvidersShareAnAccountThroughTheUnionID(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)

	mini := a.providerSignIn("wechat_miniapp", "om1.")
	assert.Nil(t, mini.user["email"])
	assert.Nil(t, mini.user["phone"])
	assert.Equal(t, []any{"user"}, mini.user["roles"])
	assert.Nil(t, a.unionID("om1"))

	again := a.providerSignIn("wechat_miniapp", "om1.u1")
	assert.Equal(t, mini.user["id"], again.user["id"])
	assert.Equal(t, "u1", *a.unionID("om1"))

	app := a.providerSignIn("wechat_app", "oa1.u1")
	assert.Equal(t, mini.user["id"], app.user["id"])

	other := a.providerSignIn("wechat_app", "oa2.u2")
	assert.NotEqual(t, mini.user["id"], other.user["id"])
}

func TestConcurrentFirstSignInsCreateOneAccount(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)

	const n = 4

	ids := make([]any, n)

	var wg sync.WaitGroup

	for i := range n {
		wg.Go(func() {
			provider := "wechat_miniapp"
			if i%2 == 1 {
				provider = "wechat_app"
			}

			r := a.providerLogin(provider, "o"+provider+".u9")
			if r.Status == http.StatusOK {
				user, _ := r.data()["user"].(map[string]any)
				ids[i] = user["id"]
			}
		})
	}

	wg.Wait()

	for i := range n {
		assert.Equal(t, ids[0], ids[i])
	}

	var users int

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(), `SELECT count(*) FROM app_user`).Scan(&users))
	assert.Equal(t, 1, users)
}

func TestProviderErrors(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)

	cases := []struct {
		provider string
		code     string
		status   int
		errCode  float64
	}{
		{"wechat_miniapp", "bad", http.StatusUnprocessableEntity, 42206},
		{"wechat_app", "down", http.StatusBadGateway, 50200},
		{"alipay", "bad", http.StatusUnprocessableEntity, 42206},
		{"alipay", "forged", http.StatusBadGateway, 50200},
		{"github", "c", http.StatusNotFound, 40401},
	}

	for _, tc := range cases {
		r := a.providerLogin(tc.provider, tc.code)
		require.Equal(t, tc.status, r.Status, "%s %s: %s", tc.provider, tc.code, r.Raw)
		assert.Equal(t, tc.errCode, r.Body["code"], tc.provider)
	}
}

func TestUnconfiguredProviderIsDisabled(t *testing.T) {
	t.Parallel()

	r := newIdentityApp(t, false).providerLogin("wechat_miniapp", "c")
	require.Equal(t, http.StatusNotFound, r.Status, r.Raw)
	assert.Equal(t, float64(40401), r.Body["code"])

	r = newIdentityApp(t, false).do(http.MethodPut, "/api/v1/me/phone/wechat", adminToken(t), `{"code":"phone.13800138000"}`)
	require.Equal(t, http.StatusNotFound, r.Status, r.Raw)
	assert.Equal(t, float64(40401), r.Body["code"])
}

func TestAlipaySignIn(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)

	first := a.providerSignIn("alipay", "2088001")
	again := a.providerSignIn("alipay", "2088001")
	assert.Equal(t, first.user["id"], again.user["id"])
}

func TestBindPhoneWithWechat(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)

	mini := a.providerSignIn("wechat_miniapp", "om1.u1")

	r := a.do(http.MethodPut, "/api/v1/me/phone/wechat", mini.access, `{"code":"phone.13800138000"}`)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Equal(t, "+8613800138000", r.data()["phone"])

	r = a.do(http.MethodPut, "/api/v1/me/phone/wechat", mini.access, `{"code":"expired"}`)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
	assert.Equal(t, float64(42206), r.Body["code"])

	other := a.providerSignIn("alipay", "2088002")

	r = a.do(http.MethodPut, "/api/v1/me/phone/wechat", other.access, `{"code":"phone.13800138000"}`)
	require.Equal(t, http.StatusConflict, r.Status, r.Raw)
	assert.Equal(t, float64(40902), r.Body["code"])
}

func TestUnionIDHeldByAnotherAccountIsNotFilledIn(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)

	app := a.providerSignIn("wechat_app", "oa1.u1")
	mini := a.providerSignIn("wechat_miniapp", "om1.")
	require.NotEqual(t, app.user["id"], mini.user["id"])

	again := a.providerSignIn("wechat_miniapp", "om1.u1")
	assert.Equal(t, mini.user["id"], again.user["id"])
	assert.Nil(t, a.unionID("om1"))

	joined := a.providerSignIn("wechat_miniapp", "om2.u1")
	assert.Equal(t, app.user["id"], joined.user["id"])
}

func TestSignInWaitingOnADeletionGetsANewAccount(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)
	first := a.providerSignIn("alipay", "2088003")
	id, _ := first.user["id"].(string)

	tx, err := a.infra.Pool.Begin(t.Context())
	require.NoError(t, err)

	_, err = tx.Exec(t.Context(), `UPDATE app_user SET deleted_at = now() WHERE id = $1`, id)
	require.NoError(t, err)

	_, err = tx.Exec(t.Context(), `DELETE FROM user_identity WHERE user_id = $1`, id)
	require.NoError(t, err)

	done := make(chan *result, 1)

	go func() { done <- a.providerLogin("alipay", "2088003") }()

	select {
	case r := <-done:
		t.Fatalf("sign-in finished before the deletion committed: %s", r.Raw)
	case <-time.After(300 * time.Millisecond):
	}

	require.NoError(t, tx.Commit(t.Context()))

	r := <-done
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.NotEqual(t, id, sessionOf(t, r).user["id"])
	assert.Equal(t, 1, a.count(`SELECT count(*) FROM session WHERE user_id = $1`, id))
}

func TestUnionIDBackfillWaitingOnADeletionGetsANewAccount(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)
	first := a.providerSignIn("wechat_miniapp", "om5.")
	id, _ := first.user["id"].(string)

	tx, err := a.infra.Pool.Begin(t.Context())
	require.NoError(t, err)

	_, err = tx.Exec(t.Context(), `SELECT id FROM app_user WHERE id = $1 FOR UPDATE`, id)
	require.NoError(t, err)

	done := make(chan *result, 1)

	go func() { done <- a.providerLogin("wechat_miniapp", "om5.u5") }()

	select {
	case r := <-done:
		t.Fatalf("sign-in finished before the deletion committed: %s", r.Raw)
	case <-time.After(300 * time.Millisecond):
	}

	_, err = tx.Exec(t.Context(), `UPDATE app_user SET deleted_at = now() WHERE id = $1`, id)
	require.NoError(t, err)

	_, err = tx.Exec(t.Context(), `DELETE FROM user_identity WHERE user_id = $1`, id)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(t.Context()))

	r := <-done
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.NotEqual(t, id, sessionOf(t, r).user["id"])
	assert.Equal(t, "u5", *a.unionID("om5"))
}
