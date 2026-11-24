//go:build integration

package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

const _phone = "+8613800138000"

// dysms is a fake Dysms SendSms endpoint that keeps the last code sent to each number.
type dysms struct {
	mu    sync.Mutex
	codes map[string]string
	fail  bool
	url   string
}

func startDysms(t *testing.T) *dysms {
	t.Helper()

	d := &dysms{codes: map[string]string{}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()

		if d.fail {
			_ = json.NewEncoder(w).Encode(map[string]string{"Code": "isv.BUSINESS_LIMIT_CONTROL", "Message": "limit"})

			return
		}

		var param map[string]string

		_ = json.Unmarshal([]byte(r.URL.Query().Get("TemplateParam")), &param)
		d.codes[r.URL.Query().Get("PhoneNumbers")] = param["code"]
		_ = json.NewEncoder(w).Encode(map[string]string{"Code": "OK", "BizId": "b"})
	}))
	t.Cleanup(srv.Close)

	d.url = srv.URL

	return d
}

// code returns the last code sent to _phone.
func (d *dysms) code(t *testing.T) string {
	t.Helper()

	d.mu.Lock()
	defer d.mu.Unlock()

	code := d.codes[_phone[len("+86"):]]
	require.NotEmpty(t, code)

	return code
}

func (d *dysms) setFail(fail bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.fail = fail
}

func (d *dysms) configure(c *config.Config) {
	c.SMS.Provider = config.SMSAliyun
	c.SMS.AccessKeyID, c.SMS.AccessKeySecret, c.SMS.SignName, c.SMS.TemplateCode = "id", "secret", "签名", "SMS_1"
	c.SMS.Endpoint = d.url
}

type smsApp struct {
	*identityApp

	sms *dysms
}

func newSMSApp(t *testing.T, limits bool) *smsApp {
	t.Helper()

	d := startDysms(t)

	return &smsApp{identityApp: newIdentityApp(t, limits, d.configure), sms: d}
}

func (a *smsApp) send(phone, scene, token string, headers ...string) *result {
	a.t.Helper()

	return a.do(http.MethodPost, "/api/v1/auth/sms/send", token, `{"phone":"`+phone+`","scene":"`+scene+`"}`, headers...)
}

func (a *smsApp) smsLogin(phone, code string, headers ...string) *result {
	a.t.Helper()

	return a.do(http.MethodPost, "/api/v1/auth/sms/login", "", `{"phone":"`+phone+`","code":"`+code+`"}`, headers...)
}

func (a *smsApp) smsSignIn(phone string) session {
	a.t.Helper()

	r := a.send(phone, "login", "")
	require.Equal(a.t, http.StatusAccepted, r.Status, r.Raw)

	r = a.smsLogin(phone, a.sms.code(a.t))
	require.Equal(a.t, http.StatusOK, r.Status, r.Raw)

	return sessionOf(a.t, r)
}

// coolDown ends the send cooldown of _phone.
func (a *smsApp) coolDown() {
	a.t.Helper()

	require.NoError(a.t, a.infra.Redis.Del(a.t.Context(), a.cfg.Telemetry.ServiceName+":sms:cooldown:"+_phone).Err())
}

func otherCode(code string) string {
	if code == "000000" {
		return "111111"
	}

	return "000000"
}

func TestSmsLoginRegistersThePhoneOnce(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)

	first := a.smsSignIn("13800138000")
	assert.Equal(t, _phone, first.user["phone"])
	assert.Nil(t, first.user["email"])
	assert.Equal(t, false, first.user["hasPassword"])
	assert.Equal(t, []any{"user"}, first.user["roles"])

	r := a.get("/api/v1/me", first.access)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Equal(t, _phone, r.data()["phone"])
}

func TestSmsCodeIsSingleUseAndBoundToItsScene(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)

	require.Equal(t, http.StatusAccepted, a.send(_phone, "login", "").Status)

	code := a.sms.code(t)

	r := a.smsLogin(_phone, code)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	r = a.smsLogin(_phone, code)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
	assert.Equal(t, float64(42201), r.Body["code"])
}

func TestSendSmsCodeCoolsDown(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)

	require.Equal(t, http.StatusAccepted, a.send(_phone, "login", "").Status)

	r := a.send(_phone, "login", "")
	require.Equal(t, http.StatusTooManyRequests, r.Status, r.Raw)
	assert.Equal(t, float64(42901), r.Body["code"])

	wait, err := strconv.Atoi(r.Header.Get("Retry-After"))
	require.NoError(t, err)
	assert.InDelta(t, 60, wait, 2)
}

func TestSendSmsCodeRejectsAnInvalidPhone(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)

	r := a.send("12345", "login", "")
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
	assert.Contains(t, r.Raw, `"field":"phone"`)
}

func TestSmsGatewayFailureIsBadGatewayAndCanBeRetried(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)
	a.sms.setFail(true)

	r := a.send(_phone, "login", "")
	require.Equal(t, http.StatusBadGateway, r.Status, r.Raw)
	assert.Equal(t, float64(50200), r.Body["code"])

	a.sms.setFail(false)
	require.Equal(t, http.StatusAccepted, a.send(_phone, "login", "").Status)
}

func TestWrongSmsCodesLockThePhone(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)

	require.Equal(t, http.StatusAccepted, a.send(_phone, "login", "").Status)

	code := a.sms.code(t)

	for range 10 {
		r := a.smsLogin(_phone, otherCode(code))
		require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
	}

	r := a.smsLogin(_phone, code)
	require.Equal(t, http.StatusTooManyRequests, r.Status, r.Raw)
	assert.Equal(t, float64(42902), r.Body["code"])
	assert.NotEmpty(t, r.Header.Get("Retry-After"))

	r = a.send(_phone, "login", "")
	require.Equal(t, http.StatusTooManyRequests, r.Status, r.Raw)
	assert.Equal(t, float64(42902), r.Body["code"])
}

func TestWrongSmsCodesAreLimitedPerAccount(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, true)

	require.Equal(t, http.StatusAccepted, a.send(_phone, "login", "", "X-Forwarded-For", "203.0.113.1").Status)

	code := a.sms.code(t)

	for i := range 5 {
		r := a.smsLogin(_phone, otherCode(code), "X-Forwarded-For", "203.0.113."+strconv.Itoa(10+i))
		require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
	}

	r := a.smsLogin(_phone, code, "X-Forwarded-For", "203.0.113.99")
	require.Equal(t, http.StatusTooManyRequests, r.Status, r.Raw)
	assert.Equal(t, float64(42900), r.Body["code"])
}

func TestSendSmsCodeIsLimitedPerIP(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, true)

	for i := range 5 {
		r := a.send("+861380013800"+strconv.Itoa(i), "login", "", "X-Forwarded-For", "203.0.113.7")
		require.Equal(t, http.StatusAccepted, r.Status, r.Raw)
	}

	r := a.send("+8613800138009", "login", "", "X-Forwarded-For", "203.0.113.7")
	require.Equal(t, http.StatusTooManyRequests, r.Status, r.Raw)
	assert.Equal(t, float64(42900), r.Body["code"])
}

func TestDisabledPhoneUserCannotSignIn(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)

	s := a.smsSignIn(_phone)
	id, _ := s.user["id"].(string)
	a.exec(`UPDATE app_user SET status = 'disabled' WHERE id = '` + id + `'`)

	a.coolDown()
	require.Equal(t, http.StatusAccepted, a.send(_phone, "login", "").Status)

	r := a.smsLogin(_phone, a.sms.code(t))
	require.Equal(t, http.StatusForbidden, r.Status, r.Raw)
	assert.Equal(t, float64(40301), r.Body["code"])
}

func TestBindPhoneCodesNeedASignedInCaller(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)

	r := a.send(_phone, "bind_phone", "")
	require.Equal(t, http.StatusUnauthorized, r.Status, r.Raw)

	r = a.send(_phone, "bind_phone", "not-a-token")
	require.Equal(t, http.StatusUnauthorized, r.Status, r.Raw)
	assert.Equal(t, float64(40100), r.Body["code"])
	assert.Contains(t, r.Header.Get("WWW-Authenticate"), `error="invalid_token"`)

	expired := testutil.IssueAccessToken(t, &testutil.TokenOptions{Roles: []string{"user"}, TTL: -time.Minute})

	r = a.send(_phone, "bind_phone", expired)
	require.Equal(t, http.StatusUnauthorized, r.Status, r.Raw)
	assert.Equal(t, float64(40101), r.Body["code"])

	require.Equal(t, http.StatusAccepted, a.send(_phone, "login", expired).Status)
	a.coolDown()

	s := a.register("ada@example.com")
	require.Equal(t, http.StatusAccepted, a.send(_phone, "bind_phone", s.access).Status)
}

func TestBindPhone(t *testing.T) {
	t.Parallel()

	a := newSMSApp(t, false)

	ada := a.register("ada@example.com")
	require.Equal(t, http.StatusAccepted, a.send(_phone, "bind_phone", ada.access).Status)

	login := a.sms.code(t)

	r := a.do(http.MethodPut, "/api/v1/me/phone", ada.access, `{"phone":"`+_phone+`","code":"`+otherCode(login)+`"}`)
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)

	r = a.do(http.MethodPut, "/api/v1/me/phone", ada.access, `{"phone":"13800138000","code":"`+login+`"}`)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Equal(t, _phone, r.data()["phone"])
	assert.Equal(t, "ada@example.com", r.data()["email"])

	bob := a.register("bob@example.com")

	a.coolDown()
	require.Equal(t, http.StatusAccepted, a.send(_phone, "bind_phone", bob.access).Status)

	r = a.do(http.MethodPut, "/api/v1/me/phone", bob.access, `{"phone":"`+_phone+`","code":"`+a.sms.code(t)+`"}`)
	require.Equal(t, http.StatusConflict, r.Status, r.Raw)
	assert.Equal(t, float64(40902), r.Body["code"])

	a.coolDown()
	require.Equal(t, http.StatusAccepted, a.send(_phone, "login", "").Status)

	r = a.smsLogin(_phone, a.sms.code(t))
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Equal(t, ada.user["id"], sessionOf(t, r).user["id"])
}
