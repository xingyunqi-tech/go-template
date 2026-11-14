//go:build integration

package app_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

const _rsaBits = 2048

var _phones atomic.Int64 //nolint:gochecknoglobals // unique phone numbers across parallel tests

func newPhone() string {
	return fmt.Sprintf("139%08d", _phones.Add(1))
}

// upstream fakes Dysms, the WeChat APIs and the Alipay gateway. WeChat and Alipay codes encode
// the answer: "o:<openid>" signs in as openid, "p:<country>:<number>" is a phone number and
// "expired" is rejected as an invalid code.
type upstream struct {
	mu        sync.Mutex
	codes     map[string]string
	smsFails  bool
	phoneHits atomic.Int32
}

func (u *upstream) code(t *testing.T, phone string) string {
	t.Helper()

	u.mu.Lock()
	defer u.mu.Unlock()

	code, ok := u.codes[phone]
	require.True(t, ok, "no code sent to %s", phone)

	return code
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

func (u *upstream) sendSMS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var params map[string]string

	_ = json.Unmarshal([]byte(q.Get("TemplateParam")), &params)

	u.mu.Lock()

	fail := u.smsFails
	if !fail {
		u.codes[q.Get("PhoneNumbers")] = params["code"]
	}
	u.mu.Unlock()

	if fail {
		writeJSON(w, map[string]string{"Code": "isv.BUSINESS_LIMIT_CONTROL", "Message": "limit"})

		return
	}

	writeJSON(w, map[string]string{"Code": "OK", "BizId": "biz"})
}

func openIDFrom(code string) (string, bool) {
	return strings.CutPrefix(code, "o:")
}

func (u *upstream) jscode2session(w http.ResponseWriter, r *http.Request) {
	openID, ok := openIDFrom(r.URL.Query().Get("js_code"))
	if !ok {
		writeJSON(w, map[string]any{"errcode": 40029, "errmsg": "invalid code"})

		return
	}

	writeJSON(w, map[string]any{"openid": openID, "session_key": "k"})
}

func (u *upstream) oauthToken(w http.ResponseWriter, r *http.Request) {
	openID, ok := openIDFrom(r.URL.Query().Get("code"))
	if !ok {
		writeJSON(w, map[string]any{"errcode": 40029, "errmsg": "invalid code"})

		return
	}

	writeJSON(w, map[string]any{"access_token": "at", "openid": openID, "unionid": "union-" + openID})
}

func (u *upstream) userInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"nickname": "WeChat Nick", "sex": 1})
}

func (u *upstream) phoneNumber(w http.ResponseWriter, r *http.Request) {
	u.phoneHits.Add(1)

	var body struct {
		Code string `json:"code"`
	}

	_ = json.NewDecoder(r.Body).Decode(&body)

	parts := strings.Split(body.Code, ":")
	if len(parts) != 3 || parts[0] != "p" {
		writeJSON(w, map[string]any{"errcode": 40029, "errmsg": "invalid code"})

		return
	}

	writeJSON(w, map[string]any{"errcode": 0, "phone_info": map[string]any{"purePhoneNumber": parts[2], "countryCode": parts[1]}})
}

func (u *upstream) gateway(w http.ResponseWriter, r *http.Request) {
	openID, ok := strings.CutPrefix(r.URL.Query().Get("code"), "ali:")
	if !ok {
		writeJSON(w, map[string]any{"error_response": map[string]any{"code": "40002", "sub_code": "isv.code-invalid"}})

		return
	}

	writeJSON(w, map[string]any{"alipay_system_oauth_token_response": map[string]any{"open_id": openID}})
}

func (u *upstream) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{$}", u.sendSMS)
	mux.HandleFunc("/sns/jscode2session", u.jscode2session)
	mux.HandleFunc("/sns/oauth2/access_token", u.oauthToken)
	mux.HandleFunc("/sns/userinfo", u.userInfo)
	mux.HandleFunc("/cgi-bin/stable_token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"access_token": "mini-token", "expires_in": 7200})
	})
	mux.HandleFunc("/wxa/business/getuserphonenumber", u.phoneNumber)
	mux.HandleFunc("/gateway.do", u.gateway)

	return mux
}

type redirect struct {
	target *url.URL
}

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host = r.target.Scheme, r.target.Host

	return http.DefaultTransport.RoundTrip(out)
}

func alipayKey(t *testing.T) string {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, _rsaBits)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	return base64.StdEncoding.EncodeToString(der)
}

// newIntegrationApp serves the API with every outbound request sent to a fake upstream.
func newIntegrationApp(t *testing.T, mutate func(*config.Config)) (*testApp, *upstream) {
	t.Helper()

	up := &upstream{mu: sync.Mutex{}, codes: map[string]string{}, smsFails: false, phoneHits: atomic.Int32{}}
	fake := httptest.NewServer(up.handler())
	t.Cleanup(fake.Close)

	target, err := url.Parse(fake.URL)
	require.NoError(t, err)

	cfg := loadConfig(t, _db.Clone(t))
	require.NoError(t, cfg.Throttle.Disabled.UnmarshalText([]byte("false")))

	cfg.SMS.AccessKeyID, cfg.SMS.AccessKeySecret = "id", "secret"
	cfg.SMS.SignName, cfg.SMS.TemplateCode = "sign", "SMS_1"
	cfg.Wechat.MiniAppID, cfg.Wechat.MiniAppSecret = "mini", "mini-secret"
	cfg.Wechat.AppID, cfg.Wechat.AppSecret = "app", "app-secret"
	cfg.Alipay.AppID, cfg.Alipay.PrivateKey = "2021", config.Secret(alipayKey(t))

	if mutate != nil {
		mutate(cfg)
	}

	infra, err := app.OpenInfra(t.Context(), cfg, 0)
	require.NoError(t, err)
	t.Cleanup(infra.Close)

	infra.Outbound = redirect{target: target}

	handler, err := app.NewHandler(cfg, slog.New(slog.DiscardHandler), infra)
	require.NoError(t, err)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &testApp{t: t, url: srv.URL, cfg: cfg, pool: infra.Pool}, up
}

func (a *testApp) post(path string, body any, headers ...string) *result {
	a.t.Helper()

	return a.do(http.MethodPost, path, "", body, headers...)
}

func (a *testApp) sendCode(phone, purpose string) {
	a.t.Helper()

	r := a.post("/api/v1/auth/phone/send-code", map[string]any{"phone": phone, "type": purpose})
	require.JSONEq(a.t, `{"code":200,"message":"success"}`, r.Raw)
}

func (a *testApp) registerPhone(up *upstream, phone string, extra map[string]any) *result {
	a.t.Helper()

	a.sendCode(phone, "register")

	body := map[string]any{"phone": phone, "code": up.code(a.t, phone), "password": _secret00}
	for k, v := range extra {
		body[k] = v
	}

	return a.post("/api/v1/auth/phone/register", body)
}

func roleClaim(t *testing.T, r *result) any {
	t.Helper()

	token, _ := r.data()["token"].(string)
	claims, err := jwtx.Verify(token, "secret")
	require.NoError(t, err)

	return claims["role"]
}

func TestSendCodeChecksPhone(t *testing.T) {
	t.Parallel()

	a, up := newIntegrationApp(t, nil)
	phone := newPhone()

	cases := []struct {
		body any
		want string
	}{
		{map[string]any{"phone": phone, "type": "change_phone"}, `{"code":422,"message":"Validation failed","data":{"type":"Unsupported verification code type"}}`},
		{map[string]any{"phone": phone}, `{"code":422,"message":"Validation failed","data":{"phone":"Phone number is not registered"}}`},
		{map[string]any{"phone": phone, "type": "reset_password"}, `{"code":422,"message":"Validation failed","data":{"phone":"Phone number is not registered"}}`},
		{map[string]any{"phone": phone, "type": "verify_identity"}, `{"code":422,"message":"Validation failed","data":{"phone":"Phone number is not registered"}}`},
	}

	for _, tc := range cases {
		assert.JSONEq(t, tc.want, a.post("/api/v1/auth/phone/send-code", tc.body).Raw)
	}

	require.Equal(t, http.StatusOK, a.registerPhone(up, phone, nil).Status)

	r := a.post("/api/v1/auth/phone/send-code", map[string]any{"phone": phone, "type": "register"}, "Accept-Language", "zh")
	assert.Equal(t, http.StatusUnprocessableEntity, r.Status)
	assert.Equal(t, map[string]any{"phone": "手机号已被注册"}, r.data())

	a.sendCode(phone, "login")

	again := a.post("/api/v1/auth/phone/send-code", map[string]any{"phone": phone})
	assert.Equal(t, http.StatusUnprocessableEntity, again.Status)
	assert.Equal(t, "Requests are too frequent, please try again later", again.data()["phone"])
	assert.Regexp(t, `"retryAfter":\d+\}`, again.Raw)
}

func TestSendCodeReportsDeliveryFailure(t *testing.T) {
	t.Parallel()

	a, up := newIntegrationApp(t, nil)
	up.smsFails = true

	r := a.post("/api/v1/auth/phone/send-code", map[string]any{"phone": newPhone(), "type": "register"})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"phone":"Failed to send verification code"}}`, r.Raw)
}

func TestSendCodeWithoutSMSAccount(t *testing.T) {
	t.Parallel()

	a, _ := newIntegrationApp(t, func(c *config.Config) { c.SMS.AccessKeyID = "" })

	r := a.post("/api/v1/auth/phone/send-code", map[string]any{"phone": newPhone(), "type": "register"})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"phone":"Failed to send verification code"}}`, r.Raw)
}

func TestPhoneRegisterAndLogins(t *testing.T) {
	t.Parallel()

	a, up := newIntegrationApp(t, nil)
	phone := newPhone()

	wrong := a.post("/api/v1/auth/phone/register", map[string]any{"phone": phone, "code": "123456", "password": _secret00})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"code":"Invalid verification code"}}`, wrong.Raw)

	r := a.registerPhone(up, phone, map[string]any{"nickname": "Phone User"})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"refreshToken":"[^"]+","token":"[^"]+","tokenExpires":\d+,"user":\{"id":\d+,`, r.Raw)

	u, _ := r.data()["user"].(map[string]any)
	assert.Equal(t, phone, u["phone"])
	assert.Equal(t, "phone", u["provider"])
	assert.Equal(t, "Phone User", u["nickname"])
	assert.Equal(t, map[string]any{"id": 2.0}, u["role"])
	assert.Equal(t, map[string]any{"id": 2.0}, roleClaim(t, r))

	login := a.post("/api/v1/auth/phone/login", map[string]any{"phone": phone, "password": strings.ToUpper(_secret00)})
	require.Equal(t, http.StatusOK, login.Status, login.Raw)
	assert.Equal(t, map[string]any{"id": 2.0, "name": "User", "__entity": "RoleEntity"}, roleClaim(t, login))

	for _, body := range []map[string]any{
		{"phone": phone, "password": md5Hex("wrong")},
		{"phone": newPhone(), "password": _secret00},
	} {
		bad := a.post("/api/v1/auth/phone/login", body)
		assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"credentials":"Incorrect phone number or password"}}`, bad.Raw)
	}

	a.sendCode(phone, "login")
	assert.Equal(t, http.StatusUnprocessableEntity, a.post("/api/v1/auth/phone/sms-login", map[string]any{"phone": phone, "code": "0000000"}).Status)

	sms := a.post("/api/v1/auth/phone/sms-login", map[string]any{"phone": phone, "code": up.code(t, phone)})
	require.Equal(t, http.StatusOK, sms.Status, sms.Raw)

	reused := a.post("/api/v1/auth/phone/sms-login", map[string]any{"phone": phone, "code": up.code(t, phone)})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"code":"Invalid verification code"}}`, reused.Raw)
}

func TestPhoneRegisterRejectsTakenPhone(t *testing.T) {
	t.Parallel()

	a, up := newIntegrationApp(t, nil)
	phone := newPhone()

	a.sendCode(phone, "register")
	code := up.code(t, phone)

	_, err := a.pool.Exec(t.Context(), `INSERT INTO "user" ("phone", "provider", "roleId", "statusId") VALUES ($1, 'email', 2, 1)`, phone)
	require.NoError(t, err)

	r := a.post("/api/v1/auth/phone/register", map[string]any{"phone": phone, "code": code, "password": _secret00})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"phone":"Phone number already exists"}}`, r.Raw)
}

func TestResetPasswordByPhone(t *testing.T) {
	t.Parallel()

	a, up := newIntegrationApp(t, nil)
	phone := newPhone()

	registered := a.registerPhone(up, phone, nil)
	require.Equal(t, http.StatusOK, registered.Status, registered.Raw)

	token, _ := registered.data()["token"].(string)
	fresh := md5Hex("NewSecret01")

	bad := a.post("/api/v1/auth/phone/reset-password", map[string]any{"phone": phone, "code": "123456", "password": fresh})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"code":"Invalid verification code"}}`, bad.Raw)

	a.sendCode(phone, "reset_password")

	r := a.post("/api/v1/auth/phone/reset-password", map[string]any{"phone": phone, "code": up.code(t, phone), "password": fresh})
	require.JSONEq(t, `{"code":200,"message":"success"}`, r.Raw)

	assert.JSONEq(t, `{"code":401,"message":"Session expired","data":null}`, a.do(http.MethodGet, "/api/v1/auth/me", token, nil).Raw)
	assert.Equal(t, http.StatusUnprocessableEntity, a.post("/api/v1/auth/phone/login", map[string]any{"phone": phone, "password": _secret00}).Status)
	assert.Equal(t, http.StatusOK, a.post("/api/v1/auth/phone/login", map[string]any{"phone": phone, "password": fresh}).Status)
}

func TestPhoneLoginRejectsBlockedUser(t *testing.T) {
	t.Parallel()

	a, up := newIntegrationApp(t, nil)
	phone := newPhone()

	require.Equal(t, http.StatusOK, a.registerPhone(up, phone, nil).Status)

	_, err := a.pool.Exec(t.Context(), `UPDATE "user" SET "statusId" = NULL WHERE "phone" = $1`, phone)
	require.NoError(t, err)

	r := a.post("/api/v1/auth/phone/login", map[string]any{"phone": phone, "password": _secret00})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"phone":"User is not active"}}`, r.Raw)
}

func TestWechatLogin(t *testing.T) {
	t.Parallel()

	a, _ := newIntegrationApp(t, nil)

	first := a.post("/api/v1/auth/wechat/login", map[string]any{"code": "o:mini-1", "type": "mini_app"})
	require.Equal(t, http.StatusOK, first.Status, first.Raw)

	u, _ := first.data()["user"].(map[string]any)
	assert.Equal(t, "微信用户", u["nickname"])
	assert.Equal(t, "wechat", u["provider"])
	assert.Equal(t, map[string]any{"id": 2.0}, roleClaim(t, first))

	again := a.post("/api/v1/auth/wechat/login", map[string]any{"code": "o:mini-1", "type": "mini_app", "nickname": "ignored"})
	require.Equal(t, http.StatusOK, again.Status, again.Raw)

	same, _ := again.data()["user"].(map[string]any)
	assert.Equal(t, u["id"], same["id"])
	assert.Equal(t, map[string]any{"id": 2.0, "name": "User", "__entity": "RoleEntity"}, roleClaim(t, again))

	openPlatform := a.post("/api/v1/auth/wechat/login", map[string]any{"code": "o:app-1", "type": "app"})
	require.Equal(t, http.StatusOK, openPlatform.Status, openPlatform.Raw)

	appUser, _ := openPlatform.data()["user"].(map[string]any)
	assert.Equal(t, "WeChat Nick", appUser["nickname"])
	assert.InDelta(t, 1, appUser["gender"], 0)

	var unionID string
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "wechatUnionId" FROM "user" WHERE "wechatOpenId" = 'app-1'`).Scan(&unionID))
	assert.Equal(t, "union-app-1", unionID)

	named := a.post("/api/v1/auth/wechat/login", map[string]any{"code": "o:app-2", "type": "app", "nickname": "Mine"})
	namedUser, _ := named.data()["user"].(map[string]any)
	assert.Equal(t, "Mine", namedUser["nickname"])

	bad := a.post("/api/v1/auth/wechat/login", map[string]any{"code": "expired", "type": "mini_app"})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"code":"Invalid verification code","wechatError":"invalid code"}}`, bad.Raw)
}

func TestWechatLoginWithoutCredentials(t *testing.T) {
	t.Parallel()

	a, _ := newIntegrationApp(t, func(c *config.Config) {
		c.Wechat.MiniAppID, c.Wechat.AppID = "", ""
		c.Alipay.AppID = ""
	})

	for _, tc := range []struct {
		path string
		body map[string]any
		want string
	}{
		{"/api/v1/auth/wechat/login", map[string]any{"code": "c", "type": "mini_app"}, `{"wechat":"Service is not configured"}`},
		{"/api/v1/auth/wechat/login", map[string]any{"code": "c", "type": "app"}, `{"wechat":"Service is not configured"}`},
		{"/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "a", "phoneCode": "b"}, `{"wechat":"Service is not configured"}`},
		{"/api/v1/auth/alipay/login", map[string]any{"authCode": "x"}, `{"alipay":"Service is not configured"}`},
	} {
		r := a.post(tc.path, tc.body)
		assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":`+tc.want+`}`, r.Raw)
	}
}

func TestWechatPhoneLoginCreatesAndBinds(t *testing.T) {
	t.Parallel()

	a, up := newIntegrationApp(t, nil)

	phone := newPhone()
	created := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:wp-1", "phoneCode": "p:86:" + phone})
	require.Equal(t, http.StatusOK, created.Status, created.Raw)

	u, _ := created.data()["user"].(map[string]any)
	assert.Equal(t, phone, u["phone"])
	assert.Equal(t, map[string]any{"id": 2.0}, roleClaim(t, created))

	again := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:wp-1", "phoneCode": "p:86:" + phone})
	require.Equal(t, http.StatusOK, again.Status, again.Raw)

	same, _ := again.data()["user"].(map[string]any)
	assert.Equal(t, u["id"], same["id"])

	byPhone := newPhone()
	require.Equal(t, http.StatusOK, a.registerPhone(up, byPhone, nil).Status)

	bound := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:wp-2", "phoneCode": "p:86:" + byPhone})
	require.Equal(t, http.StatusOK, bound.Status, bound.Raw)

	boundUser, _ := bound.data()["user"].(map[string]any)
	assert.Equal(t, "wp-2", boundUser["wechatOpenId"])
	assert.Equal(t, byPhone, boundUser["phone"])

	login := a.post("/api/v1/auth/wechat/login", map[string]any{"code": "o:wp-3", "type": "mini_app"})
	require.Equal(t, http.StatusOK, login.Status)

	addPhone := newPhone()
	withPhone := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:wp-3", "phoneCode": "p:86:" + addPhone})
	require.Equal(t, http.StatusOK, withPhone.Status, withPhone.Raw)

	withPhoneUser, _ := withPhone.data()["user"].(map[string]any)
	assert.Equal(t, addPhone, withPhoneUser["phone"])
}

func TestWechatPhoneLoginRejections(t *testing.T) {
	t.Parallel()

	a, up := newIntegrationApp(t, nil)

	phone := newPhone()
	require.Equal(t, http.StatusOK, a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:r-1", "phoneCode": "p:86:" + phone}).Status)

	other := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:r-2", "phoneCode": "p:86:" + phone})
	assert.JSONEq(t, `{"code":422,"message":"This phone number is bound to another WeChat account, please log in with an SMS code",`+
		`"data":{"phone":"This phone number is bound to another WeChat account"}}`, other.Raw)

	require.Equal(t, http.StatusOK, a.post("/api/v1/auth/wechat/login", map[string]any{"code": "o:r-3", "type": "mini_app"}).Status)

	conflict := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:r-3", "phoneCode": "p:86:" + phone})
	assert.Equal(t, http.StatusUnprocessableEntity, conflict.Status)

	foreign := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:r-4", "phoneCode": "p:1:2025550100"})
	assert.JSONEq(t, `{"code":422,"message":"Only mainland China phone numbers are supported","data":{"phone":"Only mainland China phone numbers are supported"}}`, foreign.Raw)

	expired := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "o:r-4", "phoneCode": "expired"})
	assert.JSONEq(t, `{"code":422,"message":"Phone number authorization expired, please try again","data":{"phoneCode":"Invalid verification code"}}`, expired.Raw)

	badLogin := a.post("/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "expired", "phoneCode": "expired"})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"code":"Invalid verification code","wechatError":"invalid code"}}`, badLogin.Raw)
	assert.Positive(t, up.phoneHits.Load())
}

func TestAlipayLogin(t *testing.T) {
	t.Parallel()

	a, _ := newIntegrationApp(t, nil)

	first := a.post("/api/v1/auth/alipay/login", map[string]any{"authCode": "ali:2088-1"})
	require.Equal(t, http.StatusOK, first.Status, first.Raw)

	u, _ := first.data()["user"].(map[string]any)
	assert.Equal(t, "支付宝用户", u["nickname"])
	assert.Equal(t, "alipay", u["provider"])
	assert.Equal(t, "2088-1", u["alipayOpenId"])
	assert.Equal(t, map[string]any{"id": 2.0}, roleClaim(t, first))

	again := a.post("/api/v1/auth/alipay/login", map[string]any{"authCode": "ali:2088-1"})
	require.Equal(t, http.StatusOK, again.Status, again.Raw)

	same, _ := again.data()["user"].(map[string]any)
	assert.Equal(t, u["id"], same["id"])

	_, err := a.pool.Exec(t.Context(), `UPDATE "user" SET "statusId" = NULL WHERE "alipayOpenId" = '2088-1'`)
	require.NoError(t, err)

	blocked := a.post("/api/v1/auth/alipay/login", map[string]any{"authCode": "ali:2088-1"})
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"alipay":"User is not active"}}`, blocked.Raw)

	expired := a.post("/api/v1/auth/alipay/login", map[string]any{"authCode": "expired"})
	assert.JSONEq(t, `{"code":422,"message":"Alipay authorization expired, please try again","data":{"authCode":"Invalid verification code"}}`, expired.Raw)
}
