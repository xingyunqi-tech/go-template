//go:build parity

package parity_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	_sendCode    = "/api/v1/auth/phone/send-code"
	_phoneDigits = 100_000_000
)

var _phoneSeq atomic.Int64 //nolint:gochecknoglobals // unique phone numbers per run

func newPhone() string {
	base, _ := strconv.ParseInt(_run, 10, 64)

	return fmt.Sprintf("139%08d", (base+_phoneSeq.Add(1))%_phoneDigits)
}

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}

	return def
}

// redisGet reads a key with redis-cli inside the Redis container of one side.
func redisGet(t *testing.T, container, key string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "docker", "exec", container, "redis-cli", "--raw", "GET", key).Output()
	require.NoError(t, err, "redis-cli GET %s in %s", key, container)

	return strings.TrimSpace(string(out))
}

// smsCodes returns the codes both services stored for phone: Go first, nestjs second.
func smsCodes(t *testing.T, phone, purpose string) [2]string {
	t.Helper()

	key := "cache::sms:code:" + purpose + ":" + phone
	containers := [2]string{
		env("PARITY_GO_REDIS_CONTAINER", "go-template-parity-redis-1"),
		env("PARITY_NESTJS_REDIS_CONTAINER", "go-template-parity-nestjs-redis-1"),
	}

	var codes [2]string

	for i, container := range containers {
		raw := redisGet(t, container, key)
		require.NotEmpty(t, raw, "%s has no %s", container, key)

		var entry struct {
			Value struct {
				Code string `json:"code"`
			} `json:"value"`
		}

		require.NoError(t, json.Unmarshal([]byte(raw), &entry), raw)
		codes[i] = entry.Value.Code
	}

	return codes
}

// doEach sends a different JSON body to each side: Go first, nestjs second.
func doEach(t *testing.T, method, target string, bodies [2]map[string]any) pair {
	t.Helper()

	var responses [2]*response

	for i, body := range bodies {
		raw, err := json.Marshal(body)
		require.NoError(t, err)

		opts := &requestOptions{
			headers: map[string]string{"Content-Type": "application/json"}, body: raw,
			sides: [2]side{{headers: map[string]string{}, target: ""}, {headers: map[string]string{}, target: ""}},
		}
		base := baseURL("PARITY_GO_URL", "http://localhost:3000")

		if i == 1 {
			base = baseURL("PARITY_NESTJS_URL", "http://localhost:3001")
		}

		responses[i] = send(t, base, method, target, opts, opts.sides[i])
		require.NotEqual(t, http.StatusTooManyRequests, responses[i].Status, string(responses[i].Body))
	}

	p := pair{Go: responses[0], Nestjs: responses[1]}
	requireSameRateLimitHeaders(t, p)

	return p
}

func withCodes(body map[string]any, codes [2]string) [2]map[string]any {
	var out [2]map[string]any

	for i, code := range codes {
		out[i] = map[string]any{"code": code}
		for k, v := range body {
			out[i][k] = v
		}
	}

	return out
}

func sendBoth(t *testing.T, phone, purpose string) {
	t.Helper()

	p := do(t, http.MethodPost, _sendCode, withJSON(t, map[string]any{"phone": phone, "type": purpose}))
	requireSameBody(t, p)
	require.Equal(t, http.StatusOK, p.Go.Status, string(p.Go.Body))
}

func registerPhone(t *testing.T, phone string, extra map[string]any) pair {
	t.Helper()

	sendBoth(t, phone, "register")

	body := map[string]any{"phone": phone, "password": password()}
	for k, v := range extra {
		body[k] = v
	}

	return doEach(t, http.MethodPost, "/api/v1/auth/phone/register", withCodes(body, smsCodes(t, phone, "register")))
}

func tokensOf(t *testing.T, p pair) [2]string {
	t.Helper()

	goToken, _ := dataOf(t, p.Go)["token"].(string)
	nestToken, _ := dataOf(t, p.Nestjs)["token"].(string)

	return [2]string{goToken, nestToken}
}

func TestPhoneAuthErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url  string
		body map[string]any
	}{
		{_sendCode, map[string]any{}},
		{_sendCode, map[string]any{"phone": "123", "type": "nope"}},
		{_sendCode, map[string]any{"phone": "13800000000", "type": "change_phone"}},
		{_sendCode, map[string]any{"phone": "13000000001"}},
		{_sendCode, map[string]any{"phone": "13000000001", "type": "reset_password"}},
		{_sendCode, map[string]any{"phone": "13000000001", "type": "verify_identity"}},
		{"/api/v1/auth/phone/login", map[string]any{}},
		{"/api/v1/auth/phone/login", map[string]any{"phone": "13000000001", "password": password()}},
		{"/api/v1/auth/phone/login", map[string]any{"phone": "13000000001", "password": "short"}},
		{"/api/v1/auth/phone/sms-login", map[string]any{}},
		{"/api/v1/auth/phone/sms-login", map[string]any{"phone": "13000000001", "code": "123456"}},
		{"/api/v1/auth/phone/sms-login", map[string]any{"phone": "13000000001", "code": "12"}},
		{"/api/v1/auth/phone/register", map[string]any{}},
		{"/api/v1/auth/phone/register", map[string]any{"phone": "13000000001", "code": "123456", "password": password(), "nickname": 1}},
		{"/api/v1/auth/phone/register", map[string]any{"phone": "13000000001", "code": "123456", "password": password()}},
		{"/api/v1/auth/phone/reset-password", map[string]any{}},
		{"/api/v1/auth/phone/reset-password", map[string]any{"phone": "13000000001", "code": "12ab", "password": password()}},
		{"/api/v1/auth/phone/reset-password", map[string]any{"phone": "13000000001", "code": "123456", "password": password()}},
		{"/api/v1/auth/wechat/login", map[string]any{}},
		{"/api/v1/auth/wechat/login", map[string]any{"code": "c", "type": "web"}},
		{"/api/v1/auth/wechat/login", map[string]any{"code": "c", "type": "mini_app"}},
		{"/api/v1/auth/wechat/login", map[string]any{"code": "c", "type": "app", "nickname": "n"}},
		{"/api/v1/auth/wechat/phone-login", map[string]any{}},
		{"/api/v1/auth/wechat/phone-login", map[string]any{"loginCode": "a", "phoneCode": "b"}},
		{"/api/v1/auth/alipay/login", map[string]any{}},
		{"/api/v1/auth/alipay/login", map[string]any{"authCode": "x"}},
	}

	for _, lang := range []string{"en", "zh-CN"} {
		for i, c := range cases {
			t.Run(lang+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()

				requireSameBody(t, do(t, http.MethodPost, c.url, withJSON(t, c.body), withHeader("Accept-Language", lang)))
			})
		}
	}
}

func requireSameLogin(t *testing.T, p pair) {
	t.Helper()

	require.Equal(t, p.Nestjs.Status, p.Go.Status, "go: %s\nnestjs: %s", p.Go.Body, p.Nestjs.Body)
	require.Equal(t, normalize(p.Nestjs.JSON(t), ""), normalize(p.Go.JSON(t), ""))
}

func withoutKey(m map[string]any, key string) map[string]any {
	out := make(map[string]any, len(m))

	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}

	return out
}

func TestPhoneRegisterAndLoginFlows(t *testing.T) {
	t.Parallel()

	phone := newPhone()
	registered := registerPhone(t, phone, map[string]any{"nickname": "  Phone User "})
	requireSameLogin(t, registered)

	goUser, _ := dataOf(t, registered.Go)["user"].(map[string]any)
	require.Equal(t, phone, goUser["phone"])

	sendBoth(t, phone, "login")

	again := do(t, http.MethodPost, _sendCode, withJSON(t, map[string]any{"phone": phone}))
	require.Equal(t, http.StatusUnprocessableEntity, again.Go.Status, string(again.Go.Body))
	require.Equal(t, http.StatusUnprocessableEntity, again.Nestjs.Status, string(again.Nestjs.Body))

	goBody, _ := again.Go.JSON(t).(map[string]any)
	nestBody, _ := again.Nestjs.JSON(t).(map[string]any)
	require.Equal(t, nestBody["message"], goBody["message"])
	require.Equal(t, withoutKey(dataOf(t, again.Nestjs), "retryAfter"), withoutKey(dataOf(t, again.Go), "retryAfter"))

	for _, r := range []*response{again.Go, again.Nestjs} {
		retry, ok := dataOf(t, r)["retryAfter"].(float64)
		require.True(t, ok, string(r.Body))
		require.Equal(t, float64(int(retry)), retry)
	}

	codes := smsCodes(t, phone, "login")
	requireSameBody(t, do(t, http.MethodPost, "/api/v1/auth/phone/sms-login", withJSON(t, map[string]any{"phone": phone, "code": "0000000"})))
	requireSameLogin(t, doEach(t, http.MethodPost, "/api/v1/auth/phone/sms-login", withCodes(map[string]any{"phone": phone}, codes)))

	for _, body := range []map[string]any{
		{"phone": phone, "password": md5Hex("wrong")},
		{"phone": phone, "password": strings.ToUpper(password())},
	} {
		requireSameLogin(t, do(t, http.MethodPost, "/api/v1/auth/phone/login", withJSON(t, body)))
	}
}

func TestPhoneResetPasswordFlow(t *testing.T) {
	t.Parallel()

	phone := newPhone()
	registered := registerPhone(t, phone, nil)
	tokens := tokensOf(t, registered)

	sendBoth(t, phone, "reset_password")

	fresh := md5Hex("NewSecret01")
	reset := doEach(t, http.MethodPost, "/api/v1/auth/phone/reset-password",
		withCodes(map[string]any{"phone": phone, "password": fresh}, smsCodes(t, phone, "reset_password")))
	requireSameBody(t, reset)
	require.Equal(t, http.StatusOK, reset.Go.Status, string(reset.Go.Body))

	requireSameBody(t, do(t, http.MethodGet, "/api/v1/auth/me", withTokens(tokens)))

	for _, pw := range []string{password(), fresh} {
		requireSameLogin(t, do(t, http.MethodPost, "/api/v1/auth/phone/login", withJSON(t, map[string]any{"phone": phone, "password": pw})))
	}
}

func TestPhoneRegisterExistingPhone(t *testing.T) {
	t.Parallel()

	phone := newPhone()
	registerPhone(t, phone, nil)

	requireSameBody(t, do(t, http.MethodPost, _sendCode, withJSON(t, map[string]any{"phone": phone, "type": "register"})))

	for _, lang := range []string{"en", "zh"} {
		requireSameBody(t, do(t, http.MethodPost, "/api/v1/auth/phone/register",
			withJSON(t, map[string]any{"phone": phone, "code": "123456", "password": password()}), withHeader("Accept-Language", lang)))
	}
}

func TestPhoneUserIdentityCheck(t *testing.T) {
	t.Parallel()

	phone := newPhone()
	tokens := tokensOf(t, registerPhone(t, phone, nil))

	for _, body := range []map[string]any{{}, {"smsCode": "123456"}, {"oldPassword": md5Hex("wrong")}} {
		requireSameBody(t, do(t, http.MethodDelete, "/api/v1/auth/me", withTokens(tokens), withJSON(t, body)))
	}

	sendBoth(t, phone, "verify_identity")
	requireSameBody(t, do(t, http.MethodPost, _sendCode, withJSON(t, map[string]any{"phone": phone, "type": "verify_identity"})))
}
