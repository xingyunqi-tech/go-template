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
	return &wechat.Config{MiniAppID: "mini", MiniAppSecret: "mini-secret", AppID: "app", AppSecret: "app-secret", MiniProgramState: "trial"}
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

func TestCodeExchangeWithoutOpenIDFails(t *testing.T) {
	t.Parallel()

	mini, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/jscode2session": reply(map[string]any{"session_key": "k"}),
	})
	_, err := mini.MiniAppLogin(t.Context(), "x")
	_, _, details := fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "requestFailed"}}, details)

	app, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/sns/oauth2/access_token": reply(map[string]any{"access_token": "at"}),
	})
	_, err = app.AppLogin(t.Context(), "x")
	_, _, details = fields(t, err)
	assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "requestFailed"}}, details)
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

func stableToken() http.HandlerFunc {
	return reply(map[string]any{"access_token": "mini-token", "expires_in": 7200})
}

func TestSendSubscribeMessage(t *testing.T) {
	t.Parallel()

	var (
		body  map[string]any
		token string
	)

	c, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/cgi-bin/stable_token": stableToken(),
		"/cgi-bin/message/subscribe/send": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			token = r.URL.Query().Get("access_token")
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			reply(map[string]any{"errcode": 0, "errmsg": "ok"})(w, r)
		},
	})

	require.NoError(t, c.SendSubscribeMessage(t.Context(), &wechat.SubscribeMessage{
		OpenID: "o1", TemplateID: "TPL1", Page: new("pages/order?id=1"), Data: map[string]string{"character_string1": "ORD1", "amount2": "99.00元"},
	}))
	assert.Equal(t, "mini-token", token)
	assert.Equal(t, map[string]any{
		"touser": "o1", "template_id": "TPL1", "page": "pages/order?id=1",
		"data":              map[string]any{"character_string1": map[string]any{"value": "ORD1"}, "amount2": map[string]any{"value": "99.00元"}},
		"miniprogram_state": "trial", "lang": "zh_CN",
	}, body)
}

func TestSendSubscribeMessageErrors(t *testing.T) {
	t.Parallel()

	msg := &wechat.SubscribeMessage{OpenID: "o1", TemplateID: "TPL1", Page: new(""), Data: map[string]string{}}

	cases := []struct {
		name          string
		reply         http.HandlerFunc
		message       string
		unrecoverable bool
	}{
		{
			"not subscribed", reply(map[string]any{"errcode": 43101, "errmsg": "user refuse to accept the msg"}),
			"WeChat API error 43101: user refuse to accept the msg", true,
		},
		{"invalid openid", reply(map[string]any{"errcode": 40003}), "WeChat API error 40003: ", true},
		{"invalid template", reply(map[string]any{"errcode": 40037, "errmsg": "invalid template_id"}), "WeChat API error 40037: invalid template_id", true},
		{"invalid page", reply(map[string]any{"errcode": 41030, "errmsg": "invalid page"}), "WeChat API error 41030: invalid page", true},
		{"invalid data", reply(map[string]any{"errcode": 47003, "errmsg": "argument invalid!"}), "WeChat API error 47003: argument invalid!", true},
		{"system busy", reply(map[string]any{"errcode": -1, "errmsg": "system error"}), "WeChat API error -1: system error", false},
		{"http error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }, "wechat: requestFailed", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := client(t, configured(), map[string]http.HandlerFunc{
				"/cgi-bin/stable_token": stableToken(), "/cgi-bin/message/subscribe/send": tc.reply,
			})

			err := c.SendSubscribeMessage(t.Context(), msg)
			require.EqualError(t, err, tc.message)
			assert.Equal(t, tc.unrecoverable, wechat.SubscribeUnrecoverable(err))
		})
	}

	t.Run("not configured", func(t *testing.T) {
		t.Parallel()

		c, _ := client(t, &wechat.Config{MiniAppID: "", MiniAppSecret: "", AppID: "app", AppSecret: "s", MiniProgramState: "formal"}, nil)

		err := c.SendSubscribeMessage(t.Context(), msg)
		status, _, details := fields(t, err)
		assert.Equal(t, entity.StatusUnprocessable, status)
		assert.Equal(t, []entity.Detail{{Name: "wechat", Value: "notConfigured"}}, details)
		assert.True(t, wechat.SubscribeUnrecoverable(err))
	})
}

func TestSendSubscribeMessageOmitsOnlyAMissingPage(t *testing.T) {
	t.Parallel()

	var body map[string]any

	c, _ := client(t, configured(), map[string]http.HandlerFunc{
		"/cgi-bin/stable_token": stableToken(),
		"/cgi-bin/message/subscribe/send": func(w http.ResponseWriter, r *http.Request) {
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			reply(map[string]any{"errcode": 0})(w, r)
		},
	})

	require.NoError(t, c.SendSubscribeMessage(t.Context(), &wechat.SubscribeMessage{OpenID: "o1", TemplateID: "T", Page: nil, Data: nil}))
	assert.NotContains(t, body, "page")
	assert.Equal(t, map[string]any{}, body["data"])

	body = nil

	require.NoError(t, c.SendSubscribeMessage(t.Context(), &wechat.SubscribeMessage{OpenID: "o1", TemplateID: "T", Page: new(""), Data: nil}))
	assert.Equal(t, "", body["page"])
}
