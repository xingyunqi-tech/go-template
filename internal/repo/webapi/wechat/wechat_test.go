package wechat_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/wechat"
)

type redirect struct {
	target *url.URL
}

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host = r.target.Scheme, r.target.Host

	return http.DefaultTransport.RoundTrip(out)
}

func client(t *testing.T, cfg *wechat.Config, routes map[string]http.HandlerFunc) (*wechat.Client, *redis.Memory) {
	t.Helper()

	mux := http.NewServeMux()
	for path, h := range routes {
		mux.HandleFunc(path, h)
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	require.NoError(t, err)

	cache := redis.NewMemory()

	return wechat.New(cfg, cache, redirect{target: target}, slog.New(slog.DiscardHandler)), cache
}

func configured() *wechat.Config {
	return &wechat.Config{MiniAppID: "mini", MiniAppSecret: "mini-secret", AppID: "app", AppSecret: "app-secret"}
}

func reply(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func fields(t *testing.T, err error) (status int, key string, details []entity.Detail) {
	t.Helper()

	var e *entity.Error
	require.ErrorAs(t, err, &e)

	return e.Status, e.ErrorKey, e.Fields
}

func TestMiniAppLogin(t *testing.T) {
	t.Parallel()

	c, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/jscode2session": func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			assert.Equal(t, "mini", q.Get("appid"))
			assert.Equal(t, "code-1", q.Get("js_code"))
			assert.Equal(t, "authorization_code", q.Get("grant_type"))
			reply(map[string]any{"openid": "o1", "unionid": "u1", "session_key": "k"})(w, r)
		},
	})

	u, err := c.MiniAppLogin(t.Context(), "code-1")
	require.NoError(t, err)
	assert.Equal(t, "o1", u.OpenID)
	require.NotNil(t, u.UnionID)
	assert.Equal(t, "u1", *u.UnionID)
	assert.Nil(t, u.Nickname)
}

func TestMiniAppLoginErrors(t *testing.T) {
	t.Parallel()

	invalid, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/jscode2session": reply(map[string]any{"errcode": 40029, "errmsg": "invalid code"}),
	})
	_, err := invalid.MiniAppLogin(t.Context(), "x")
	status, _, details := fields(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, []entity.Detail{{Name: "code", Value: "invalidCode"}, {Name: "wechatError", Value: "invalid code"}}, details)

	failing, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/jscode2session": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
	})
	_, err = failing.MiniAppLogin(t.Context(), "x")
	_, _, details = fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "requestFailed"}}, details)

	garbled, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/jscode2session": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
	})
	_, err = garbled.MiniAppLogin(t.Context(), "x")
	_, _, details = fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "requestFailed"}}, details)

	missing, _ := client(t, &wechat.Config{MiniAppID: "", MiniAppSecret: "", AppID: "", AppSecret: ""}, nil)
	_, err = missing.MiniAppLogin(t.Context(), "x")
	_, _, details = fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "notConfigured"}}, details)

	_, err = missing.AppLogin(t.Context(), "x")
	_, _, details = fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "notConfigured"}}, details)

	_, err = missing.PhoneNumber(t.Context(), "x")
	_, _, details = fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "notConfigured"}}, details)
}

func TestAppLogin(t *testing.T) {
	t.Parallel()

	c, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/oauth2/access_token": reply(map[string]any{"access_token": "at", "openid": "o2", "unionid": "u-token"}),
		"/sns/userinfo": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "at", r.URL.Query().Get("access_token"))
			assert.Equal(t, "o2", r.URL.Query().Get("openid"))
			reply(map[string]any{"nickname": "Nick", "headimgurl": "h", "sex": 1})(w, r)
		},
	})

	u, err := c.AppLogin(t.Context(), "code")
	require.NoError(t, err)
	assert.Equal(t, "o2", u.OpenID)
	assert.Equal(t, "u-token", *u.UnionID)
	assert.Equal(t, "Nick", *u.Nickname)
	assert.Equal(t, 1, *u.Gender)

	broken, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/oauth2/access_token": reply(map[string]any{"access_token": "at", "openid": "o2"}),
		"/sns/userinfo":            reply(map[string]any{"errcode": 40003, "errmsg": "bad openid"}),
	})
	_, err = broken.AppLogin(t.Context(), "code")
	_, _, details := fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "userInfoFailed"}, {Name: "wechatError", Value: "bad openid"}}, details)

	rejected, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/oauth2/access_token": reply(map[string]any{"errcode": 40029}),
	})
	_, err = rejected.AppLogin(t.Context(), "code")
	_, _, details = fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "code", Value: "invalidCode"}}, details)
}

func TestPhoneNumberCachesAndRefreshesToken(t *testing.T) {
	t.Parallel()

	var tokens atomic.Int32

	c, cache := client(t, configured(), map[string]http.HandlerFunc{
		"/cgi-bin/stable_token": func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "mini", body["appid"])

			n := tokens.Add(1)
			reply(map[string]any{"access_token": "t" + strconv.Itoa(int(n)), "expires_in": 7200})(w, r)
		},
		"/wxa/business/getuserphonenumber": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("access_token") == "t1" {
				reply(map[string]any{"errcode": 40001, "errmsg": "invalid credential"})(w, r)

				return
			}

			reply(map[string]any{"errcode": 0, "phone_info": map[string]any{"purePhoneNumber": "13800138000", "countryCode": "86"}})(w, r)
		},
	})

	p, err := c.PhoneNumber(t.Context(), "pc")
	require.NoError(t, err)
	assert.Equal(t, entity.WechatPhone{Number: "13800138000", CountryCode: "86"}, *p)
	assert.Equal(t, int32(2), tokens.Load())

	var cached string

	found, err := cache.Get(t.Context(), wechat.AccessTokenCacheKey, &cached)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "t2", cached)

	_, err = c.PhoneNumber(t.Context(), "pc")
	require.NoError(t, err)
	assert.Equal(t, int32(2), tokens.Load())
}

func TestPhoneNumberErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		reply   map[string]any
		key     string
		details []entity.Detail
	}{
		{"invalid code", map[string]any{"errcode": 40029, "errmsg": "invalid code"}, "invalidPhoneCode", []entity.Detail{{Name: "phoneCode", Value: "invalidCode"}}},
		{"used code", map[string]any{"errcode": 40163}, "invalidPhoneCode", []entity.Detail{{Name: "phoneCode", Value: "invalidCode"}}},
		{"other error", map[string]any{"errcode": 45009}, "", []entity.Detail{{Name: "wechat", Value: "requestFailed"}}},
		{"no phone", map[string]any{"errcode": 0}, "", []entity.Detail{{Name: "wechat", Value: "requestFailed"}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, cache := client(t, configured(), map[string]http.HandlerFunc{
				"/wxa/business/getuserphonenumber": reply(tc.reply),
			})
			require.NoError(t, cache.Set(t.Context(), wechat.AccessTokenCacheKey, "cached", time.Minute))

			_, err := c.PhoneNumber(t.Context(), "pc")
			status, key, details := fields(t, err)
			assert.Equal(t, http.StatusUnprocessableEntity, status)
			assert.Equal(t, tc.key, key)
			assert.Equal(t, tc.details, details)
		})
	}
}

func TestPhoneNumberTokenFailure(t *testing.T) {
	t.Parallel()

	c, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/cgi-bin/stable_token": reply(map[string]any{"errcode": 40013, "errmsg": "invalid appid"}),
	})

	_, err := c.PhoneNumber(t.Context(), "pc")
	_, _, details := fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "requestFailed"}}, details)
}
